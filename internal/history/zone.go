package history

import (
	"sync"
	"time"

	// History dates follow the host's IANA time zone with its full rules, including past DST changes. End-user machines have
	// no Go installation and no zoneinfo.zip, so the executable carries the IANA database that ships with the Go toolchain.
	_ "time/tzdata"
)

// History time zones.
//
// Storage is UTC: every stored instant is the capture clock in UTC nanoseconds and never changes. The zone a History view
// lays dates out in is the host's current zone, never the zone an encounter was recorded in: a fight at 18:30 UTC belongs to
// the next calendar day in Tokyo and to the same day in Los Angeles, and the heatmap, the daily list, the encounter header
// and every clock agree because Go converts all of them with the same zone. The date and offset recorded with an
// encounter (local_date, utc_offset_s) are provenance only and never decide where a fight is shown.
//
// The view names its zone by its IANA name (the WebView reports the zone Windows is set to, Intl resolvedOptions); Go loads
// it with full rules from the embedded database. A name that cannot be loaded falls back to the zone Go read from Windows
// at start (time.Local) and says so, so the UI never silently shows dates of a zone it did not ask for.

// ViewZone is the zone a History view lays dates and clocks out in.
type ViewZone struct {
	Location *time.Location
	// ID is the IANA name the zone was loaded by; "Local" when the zone Go read from Windows at start was used.
	ID string
	// Fallback is set when the requested zone could not be used and Local was used instead.
	Fallback bool
}

// LocalZoneID names the zone Go read from Windows when the process started.
const LocalZoneID = "Local"

// maxZoneName bounds a requested zone name; the longest IANA name is about 30 characters.
const maxZoneName = 64

// maxCachedZones bounds the loaded zones kept: a view asks for one zone, and a change of the Windows zone adds one more.
const maxCachedZones = 16

var zoneCache = struct {
	sync.Mutex
	loaded map[string]*time.Location
}{loaded: map[string]*time.Location{}}

// ResolveViewZone loads the IANA zone name. An empty, oversized or unknown name falls back to Local.
func ResolveViewZone(name string) ViewZone {
	if name == LocalZoneID {
		return ViewZone{Location: time.Local, ID: LocalZoneID}
	}
	if name == "" || len(name) > maxZoneName {
		return ViewZone{Location: time.Local, ID: LocalZoneID, Fallback: true}
	}
	zoneCache.Lock()
	location, ok := zoneCache.loaded[name]
	zoneCache.Unlock()
	if !ok {
		var err error
		// LoadLocation refuses path-like names, so a name only ever selects an entry of the embedded database.
		if location, err = time.LoadLocation(name); err != nil {
			return ViewZone{Location: time.Local, ID: LocalZoneID, Fallback: true}
		}
		zoneCache.Lock()
		if len(zoneCache.loaded) < maxCachedZones {
			zoneCache.loaded[name] = location
		}
		zoneCache.Unlock()
	}
	return ViewZone{Location: location, ID: name}
}

// viewLocation is the zone a query lays dates out in; nil means the zone Go read from Windows at start.
func viewLocation(zone *time.Location) *time.Location {
	if zone == nil {
		return time.Local
	}
	return zone
}

// LocalTime is one stored instant laid out in the view's zone: its calendar date and clock there, and that zone's UTC offset
// at the instant, which differs across a DST change within one day.
type LocalTime struct {
	Date          string `json:"date"`
	Clock         string `json:"clock"`
	OffsetSeconds int    `json:"offsetSeconds"`
}

const (
	dateLayout  = "2006-01-02"
	clockLayout = "15:04:05"
)

func localTimeOf(at time.Time, zone *time.Location) LocalTime {
	local := at.In(zone)
	_, offset := local.Zone()
	return LocalTime{Date: local.Format(dateLayout), Clock: local.Format(clockLayout), OffsetSeconds: offset}
}

// RecordedZone is the calendar date and UTC offset of the host where an encounter started, as recorded. It is provenance:
// it never decides the date a view shows.
type RecordedZone struct {
	Date          string `json:"date"`
	OffsetSeconds int    `json:"offsetSeconds"`
}

// dateSearchMargin widens a UTC range so that it holds every instant whose date in any zone falls inside it: UTC offsets lie
// between -12 and +14 hours, so a calendar date of one zone spans at most 26 hours of another zone's dates.
const dateSearchMargin = 48 * time.Hour

// recordedDateMargin widens the same range in recorded dates, in days. An encounter's recorded date (local_date) is the UTC
// date of its start shifted by the offset it was recorded at, at most 14 hours either way, so one day on each side already
// holds it. The margin only lets a query reach the local_date index; the stored UTC instant alone decides where an
// encounter is shown.
const recordedDateMargin = 3

// dateBounds is the UTC range and the recorded-date range a query reads for the instants of [from, to). Both are supersets;
// callers keep only the instants whose date in the view's zone they want.
func dateBounds(from, to time.Time) (fromNs, toNs int64, fromDate, toDate string) {
	from, to = from.Add(-dateSearchMargin), to.Add(dateSearchMargin)
	return from.UnixNano(), to.UnixNano(), from.UTC().AddDate(0, 0, -recordedDateMargin).Format(dateLayout), to.UTC().AddDate(0, 0, recordedDateMargin).Format(dateLayout)
}
