// Command server is the barbershop backend. One binary, several roles:
//
//	server api       serve the HTTP API (default)
//	server worker    deliver domain events between modules (outbox jobs on River)
//	server migrate   apply pending database migrations, then exit
//
// api and worker build the same modules; they differ only in what they run.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // branch time zones work even on an image without /usr/share/zoneinfo

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking"
	bookinghttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	businesshttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog"
	cataloghttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery"
	discoveryhttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam"
	iamhttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media"
	mediahttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/logging"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling"
	schedulinghttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// version is stamped at build time by the Dockerfile and the Makefile:
//
//	go build -ldflags "-X main.version=v0.3.0" ./cmd/server
//
// A plain `go run` leaves it as "dev".
var version = "dev"

func main() {
	// main stays tiny: everything that can fail lives in run, which returns
	// an error instead of calling os.Exit, so it's testable and defers run.
	if err := run(context.Background(), os.Args[1:], os.Environ(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run wires the application. Its inputs are explicit (args, environment,
// output) instead of globals — the whole dependency graph is visible here.
func run(ctx context.Context, args, environ []string, stdout io.Writer) error {
	// The context is cancelled on Ctrl-C or SIGTERM (docker stop, deploys);
	// everything below watches it to shut down cleanly.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, stop listening: a second Ctrl-C then kills the
	// process the default way instead of waiting out the graceful shutdown.
	context.AfterFunc(ctx, stop)

	role := "api"
	if len(args) > 0 {
		role = args[0]
	}
	if role != "api" && role != "worker" && role != "migrate" {
		return errors.New("unknown command (want: api, worker, migrate)")
	}

	cfg, err := config.Load(environ)
	if err != nil {
		return err
	}
	logger := logging.New(stdout, cfg.Log, cfg.Env)
	logger.InfoContext(ctx, "starting", slog.String("role", role), slog.String("version", version))

	dbCfg := cfg.Database
	if role == "migrate" {
		// Migrations may rewrite a table or wait for traffic to let go of
		// one: no statement or lock limits, and they run one at a time.
		dbCfg.StatementTimeout, dbCfg.LockTimeout, dbCfg.IdleInTxTimeout = 0, 0, 0
	}
	pool, err := database.Open(ctx, dbCfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	if role == "migrate" {
		return database.Migrate(ctx, pool, logger)
	}

	a, err := newApplication(cfg, pool, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := a.close(); err != nil {
			logger.WarnContext(ctx, "closing the application", slog.String("error_type", fmt.Sprintf("%T", err)))
		}
	}()
	if role == "worker" {
		return a.bus.Run(ctx, cfg.Worker.StopTimeout)
	}
	return serveAPI(ctx, cfg.HTTP, a.handler, logger)
}

// apiServer is the whole API: one embedded handler set per module. Embedding
// promotes each module's methods, so together they satisfy the generated
// apigen.StrictServerInterface. New modules add a line here.
//
// Every module calls its type Handlers, and an embedded field is named after
// its type, so two of them would clash; the aliases give each a distinct name.
type (
	iamAPI        = iamhttp.Handlers
	businessAPI   = businesshttp.Handlers
	mediaAPI      = mediahttp.Handlers
	catalogAPI    = cataloghttp.Handlers
	schedulingAPI = schedulinghttp.Handlers
	bookingAPI    = bookinghttp.Handlers
	discoveryAPI  = discoveryhttp.Handlers
)

type apiServer struct {
	*iamAPI        // iam: /v1/auth/*, /v1/me
	*businessAPI   // business: /v1/businesses/* (incl. branches, staff), /v1/invitations/accept, /v1/me/memberships
	*mediaAPI      // media: /v1/media/* (signed downloads)
	*catalogAPI    // catalog: /v1/service-categories, /v1/businesses/{id}/branches/{id}/services
	*schedulingAPI // scheduling: /v1/businesses/{id}/branches/{id}/opening-hours
	*bookingAPI    // booking: /v1/branches/{id}/availability
	*discoveryAPI  // discovery: /v1/branches, /v1/cities
}

// application is every module, wired: the API for the api role, the outbox
// for the worker role.
type application struct {
	handler http.Handler
	bus     *outbox.Bus
	close   func() error // releases what the modules hold open (the media directory)
}

// newApplication builds every module, subscribes them to each other's
// events, and mounts the API next to the health checks.
func newApplication(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (*application, error) {
	bus, err := outbox.New(pool, logger)
	if err != nil {
		return nil, err
	}
	iamModule, err := iam.New(iam.Deps{
		Pool:        pool,
		Clock:       clock.System{},
		Logger:      logger,
		OTPSecret:   []byte(cfg.Auth.OTPSecret),
		TokenSecret: []byte(cfg.Auth.TokenSecret),
	})
	if err != nil {
		return nil, err
	}

	mediaModule, err := media.New(media.Deps{
		Pool: pool, Clock: clock.System{}, Logger: logger,
		Dir: cfg.Media.Dir, Secret: []byte(cfg.Media.SigningSecret),
	})
	if err != nil {
		return nil, err
	}
	billingModule := billing.New(billing.Deps{Pool: pool, Clock: clock.System{}, Logger: logger})
	readiness := &branchReadiness{} // filled in below, once catalog and scheduling exist
	businessModule := business.New(business.Deps{
		Pool: pool, Clock: clock.System{}, Logger: logger,
		Media: mediaModule, Users: iamModule, Billing: billingModule, Events: bus, Readiness: readiness,
	})
	catalogModule := catalog.New(catalog.Deps{Pool: pool, Clock: clock.System{}, Logger: logger, Business: businessModule, Events: bus})
	schedulingModule := scheduling.New(scheduling.Deps{Pool: pool, Clock: clock.System{}, Logger: logger, Business: businessModule})
	readiness.catalog, readiness.scheduling = catalogModule, schedulingModule
	bookingModule := booking.New(booking.Deps{
		Pool: pool, Clock: clock.System{}, Logger: logger,
		Business: businessModule, Catalog: catalogModule, Scheduling: schedulingModule, Events: bus,
	})
	discoveryModule := discovery.New(discovery.Deps{Pool: pool, Logger: logger})
	subscribe(bus, billingModule, discoveryModule)

	router := httpx.NewRouter(logger, httpx.NewHealth(pool, logger))
	api := apiServer{
		iamModule.HTTP(), businessModule.HTTP(), mediaModule.HTTP(), catalogModule.HTTP(), schedulingModule.HTTP(),
		bookingModule.HTTP(), discoveryModule.HTTP(),
	}
	if err := httpx.MountAPI(router, api, logger, iamModule.Authenticate); err != nil {
		return nil, err
	}
	return &application{handler: router, bus: bus, close: mediaModule.Close}, nil
}

// branchReadiness answers business's question "could this branch take a
// booking?" from catalog and scheduling. business can't ask them itself —
// they depend on it, and Go forbids import cycles — so main, which sees
// every module, puts the answer together.
type branchReadiness struct {
	catalog    *catalog.Module
	scheduling *scheduling.Module
}

func (r *branchReadiness) BranchReadiness(ctx context.Context, biz shared.BusinessID, branch shared.BranchID) (business.Readiness, error) {
	performers, err := r.catalog.PerformingStaff(ctx, biz, branch)
	if err != nil {
		return business.Readiness{}, err
	}
	hours, scheduled, err := r.scheduling.Readiness(ctx, biz, branch, performers)
	if err != nil {
		return business.Readiness{}, err
	}
	return business.Readiness{OpeningHours: hours, OfferedService: len(performers) > 0, BookableBarber: len(scheduled) > 0}, nil
}

// subscribe wires who reacts to which event. Subscriber names are stored in
// queued jobs: never rename one (add a new name and retire the old).
func subscribe(bus *outbox.Bus, billingModule *billing.Module, discoveryModule *discovery.Module) {
	// An approved business starts its free trial.
	business.OnApproved(bus, "billing.start_trial", func(ctx context.Context, e business.Approved) error {
		return billingModule.StartTrial(ctx, e.BusinessID, e.ApprovedAt)
	})
	// Discovery keeps its copy of every branch: published, unpublished or
	// edited. The two types have the same fields, so a conversion does.
	business.OnBranchChanged(bus, "discovery.keep_branch", func(ctx context.Context, e business.BranchChanged) error {
		return discoveryModule.KeepBranch(ctx, discovery.Branch(e))
	})
	// And of what each branch sells, for the category filter and the price
	// from: services added or changed, turned off, or given barbers.
	catalog.OnServiceChanged(bus, "discovery.keep_service", func(ctx context.Context, e catalog.ServiceChanged) error {
		return discoveryModule.KeepService(ctx, discovery.Service(e))
	})
}

func serveAPI(ctx context.Context, cfg config.HTTP, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	logger.InfoContext(ctx, "http server listening", slog.String("addr", ln.Addr().String()))

	return httpx.Serve(ctx, srv, ln, cfg.ShutdownTimeout, logger)
}
