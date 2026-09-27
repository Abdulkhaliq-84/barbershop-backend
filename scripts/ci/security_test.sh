#!/usr/bin/env bash
# Exercise release scripts without credentials, writes to a registry, or tags.
set -euo pipefail
python3 - <<'PY'
import os
from pathlib import Path
import subprocess
import tempfile

with tempfile.TemporaryDirectory() as work:
    root = Path(work)
    fake = root / 'bin'
    fake.mkdir()
    digest = 'sha256:' + 'a' * 64
    different = 'sha256:' + 'b' * 64
    sha = 'c' * 40
    stub = '''#!/usr/bin/env python3
import os,sys,json
from pathlib import Path
name=Path(sys.argv[0]).name
a=sys.argv[1:]
if name=='docker':
 if a[:3]==['buildx','imagetools','inspect']:
  if os.environ.get('ERROR'):
   print(os.environ['ERROR'],file=sys.stderr);sys.exit(1)
  tag=a[3]
  value=os.environ.get('SOURCE',os.environ['DIGEST']) if ':sha-' in tag else os.environ.get('EXISTING','')
  if Path(os.environ['STATE']).exists():value=os.environ['DIGEST']
  if value:
   assert a[4:]==['--format','{{json .Manifest}}']
   print(json.dumps({'digest':value}))
  else: print('ERROR: '+tag+': not found',file=sys.stderr);sys.exit(1)
 else:
  with open(os.environ['CALLS'],'a') as f:f.write(' '.join(a)+'\\n')
  Path(os.environ['STATE']).touch()
elif name=='git':
 if a[0]=='tag':print(os.environ.get('TAGS','v1.2.3'))
 elif 'refs/heads/main' in a:print(os.environ.get('MAIN',os.environ['GITHUB_SHA'])+'\\trefs/heads/main')
 elif os.environ.get('ANNOTATED'):
  print('d'*40+'\\trefs/tags/v1.2.3')
  print(os.environ.get('COMMIT',os.environ['GITHUB_SHA'])+'\\trefs/tags/v1.2.3^{}')
 else:print(os.environ.get('COMMIT',os.environ['GITHUB_SHA'])+'\\trefs/tags/v1.2.3')
elif name=='gh':
 if os.environ.get('API_FAIL'):sys.exit(1)
 print(os.environ.get('PUBLISHED','v1.2.3'))
'''
    for name in ['docker', 'git', 'gh']:
        path = fake / name
        path.write_text(stub)
        path.chmod(0o755)
    base = dict(os.environ, PATH=str(fake)+os.pathsep+os.environ['PATH'], IMAGE='registry/image',
                DIGEST=digest, TAG='v1.2.3', GITHUB_SHA=sha, GITHUB_REPOSITORY='owner/repo',
                STATE=str(root/'state'), CALLS=str(root/'calls'), GITHUB_OUTPUT=str(root/'output'))
    count = 0
    def run(script='promote', ok=True, **values):
        global count
        for file in ['state','calls','output']:
            (root/file).unlink(missing_ok=True)
        result = subprocess.run(['bash',f'scripts/ci/{script}.sh'],env=dict(base,**values),capture_output=True,text=True)
        assert (result.returncode == 0) == ok, (script,values,result.stdout,result.stderr)
        count += 1
        return (root/'calls').read_text() if (root/'calls').exists() else ''
    assert ':v1.2.3' in run()
    assert ':v1.2.3' in run(ANNOTATED='1')
    calls=run(EXISTING=digest)
    assert ':v1.2.3' not in calls and ':latest' in calls
    assert run(EXISTING=different,ok=False)==''
    assert run(SOURCE=different,ok=False)==''
    assert run(COMMIT='e'*40,ok=False)==''
    assert run(TAG='v1.2.3;echo injected',ok=False)==''
    assert run(TAG='v01.2.3',ok=False)==''
    assert run(DIGEST='invalid',ok=False)==''
    for error in ['unauthorized','connection refused','500 Internal Server Error','lookup registry: host not found']:
        assert run(ERROR=error,ok=False)==''
    assert ':latest' not in run(MAIN='e'*40)
    run('release-tag',CREATED_TAG='v1.2.3')
    assert (root/'output').read_text()=='tag=v1.2.3\n'
    run('release-tag',CREATED_TAG='') # interrupted-release retry
    assert (root/'output').read_text()=='tag=v1.2.3\n'
    run('release-tag',TAGS='',PUBLISHED='')
    assert not (root/'output').exists()
    run('release-tag',CREATED_TAG='v9.9.9',ok=False)
    run('release-tag',TAGS='v1.2.3\nv1.2.4',PUBLISHED='v1.2.3\nv1.2.4',ok=False)
    run('release-tag',API_FAIL='1',ok=False)
    print(f'{count} release guard scenarios passed')
PY
