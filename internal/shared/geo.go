package shared

import (
	"errors"
	"fmt"
	"math"
)

// ErrInvalidCoordinates reports a latitude/longitude outside the globe.
var ErrInvalidCoordinates = errors.New("invalid coordinates")

// GeoPoint is a WGS 84 location (the GPS coordinate system Google Maps and
// PostGIS geography use). Distances and radius search happen in PostGIS.
type GeoPoint struct {
	lat float64
	lng float64
}

// NewGeoPoint validates latitude (-90..90) and longitude (-180..180).
func NewGeoPoint(lat, lng float64) (GeoPoint, error) {
	if !isFinite(lat) || !isFinite(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return GeoPoint{}, fmt.Errorf("%w: (%v, %v)", ErrInvalidCoordinates, lat, lng)
	}
	return GeoPoint{lat: lat, lng: lng}, nil
}

// Lat returns the latitude in degrees.
func (p GeoPoint) Lat() float64 { return p.lat }

// Lng returns the longitude in degrees.
func (p GeoPoint) Lng() float64 { return p.lng }

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
