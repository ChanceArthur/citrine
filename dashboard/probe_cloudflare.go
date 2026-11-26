package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const cfGraphQL = "https://api.cloudflare.com/client/v4/graphql"

const userAgent = "citrine-dashboard"

var errNoToken = errors.New("no CF_API_TOKEN or CF_ZONE_ID in secrets.env")

type shieldEvent struct {
	at      time.Time
	source  string
	what    string
	rule    string
	ray     string
	ip      string
	asn     string
	country string
	method  string
	host    string
	path    string
	agent   string
}

type traffic struct {
	requests    int
	visits      int
	pageViews   int
	bytes       uint64
	cachedBytes uint64
	cachedReqs  int
	stopped     int
	status4xx   float64
	status5xx   float64
	originP50   float64
	originP95   float64
	hist        []float64
	histStopped []float64
	lastServed  time.Time
	countries   []count
	paths       []count
	statuses    []count
	cache       []count
	security    []count
	bots        []count
}

func trafficStep(window time.Duration) string {
	switch {
	case window <= time.Hour:
		return "datetimeMinute"
	case window <= 24*time.Hour:
		return "datetimeFifteenMinutes"
	}
	return "datetimeHour"
}

const (
	windowed      = "datetime_geq: $since, datetime_lt: $until"
	eyeball       = windowed + `, requestSource: "eyeball"`
	shieldBlocked = eyeball + `, edgeResponseStatus: 403, securitySource: "unknown"`
	served        = eyeball + `, AND: [{OR: [{securitySource: "unknown"}, {securityAction: "link_maze_injected"}]}, {OR: [{edgeResponseStatus_neq: 403}, {securitySource_neq: "unknown"}]}]`
	stopped       = eyeball + `, OR: [{securitySource_neq: "unknown", securityAction_neq: "link_maze_injected"}, {edgeResponseStatus: 403, securitySource: "unknown"}]`
	stopsLimit    = 1000
)

func trafficQuery(window time.Duration) string {
	step := trafficStep(window)
	return fmt.Sprintf(`query($zone: string!, $since: Time!, $until: Time!) {
  viewer { zones(filter: {zoneTag: $zone}) {
    overview: httpRequestsOverviewAdaptiveGroups(limit: 1, filter: {%[1]s}) { sum { requests visits pageViews bytes cachedBytes cachedRequests } }
    shieldBlocked: httpRequestsAdaptiveGroups(limit: 1, filter: {%[4]s}) { count }
    total: httpRequestsAdaptiveGroups(limit: 1, filter: {%[2]s}) { ratio { status4xx status5xx } }
    origin: httpRequestsAdaptiveGroups(limit: 1, filter: {%[1]s, originResponseDurationMs_gt: 0}) { quantiles { p50: originResponseDurationMsP50 p95: originResponseDurationMsP95 } }
    hist: httpRequestsAdaptiveGroups(limit: 1000, filter: {%[2]s}, orderBy: [%[5]s_ASC]) { count dimensions { t: %[5]s } }
    histStopped: httpRequestsAdaptiveGroups(limit: 1000, filter: {%[3]s}, orderBy: [%[5]s_ASC]) { count dimensions { t: %[5]s } }
    fetched: httpRequestsAdaptiveGroups(limit: 1, filter: {%[2]s, cacheStatus_neq: "none"}, orderBy: [datetime_DESC]) { dimensions { t: datetime } }
    countries: httpRequestsAdaptiveGroups(limit: 5, filter: {%[2]s}, orderBy: [count_DESC]) { count dimensions { label: clientCountryName } }
    paths: httpRequestsAdaptiveGroups(limit: 5, filter: {%[2]s, securitySource: "unknown"}, orderBy: [count_DESC]) { count dimensions { label: clientRequestPath } }
    statuses: httpRequestsAdaptiveGroups(limit: 5, filter: {%[2]s}, orderBy: [count_DESC]) { count dimensions { label: edgeResponseStatus } }
    cache: httpRequestsAdaptiveGroups(limit: 5, filter: {%[2]s}, orderBy: [count_DESC]) { count dimensions { label: cacheStatus } }
    security: httpRequestsAdaptiveGroups(limit: 5, filter: {%[1]s, securitySource_neq: "unknown"}, orderBy: [count_DESC]) { count dimensions { action: securityAction source: securitySource } }
    bots: httpRequestsAdaptiveGroups(limit: 5, filter: {%[2]s, verifiedBotCategory_neq: ""}, orderBy: [count_DESC]) { count dimensions { label: verifiedBotCategory } }
  } }
}`, windowed, served, stopped, shieldBlocked, step)
}

var stopsQuery = fmt.Sprintf(`query($zone: string!, $since: Time!, $until: Time!) {
  viewer { zones(filter: {zoneTag: $zone}) {
    events: firewallEventsAdaptive(limit: %[1]d, filter: {datetime_geq: $since, datetime_lt: $until, action_neq: "link_maze_injected"}, orderBy: [datetime_DESC]) {
      datetime action source description ruleId rayName clientIP clientAsn clientASNDescription clientCountryName
      clientRequestHTTPMethodName clientRequestHTTPHost clientRequestPath clientRequestQuery userAgent
    }
    shieldBlocked: httpRequestsAdaptiveGroups(limit: %[1]d, filter: {%[2]s}, orderBy: [datetime_DESC]) {
      dimensions { datetime clientIP clientCountryName clientRequestHTTPMethodName clientRequestHTTPHost clientRequestPath userAgent }
    }
  } }
}`, stopsLimit, shieldBlocked)

func cfAsk(token, zone, query string, window time.Duration) ([]byte, time.Time, error) {
	if token == "" || zone == "" {
		return nil, time.Time{}, errNoToken
	}
	until := time.Now().UTC().Truncate(time.Minute)
	since := until.Add(-window)
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]string{"zone": zone, "since": since.Format(time.RFC3339), "until": until.Format(time.RFC3339)},
	})
	if err != nil {
		return nil, since, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfGraphQL, bytes.NewReader(body))
	if err != nil {
		return nil, since, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, since, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, since, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, since, fmt.Errorf("Cloudflare answered %s", resp.Status)
	}
	return b, since, nil
}

func readTraffic(token, zone string, window time.Duration, buckets int) (traffic, error) {
	b, since, err := cfAsk(token, zone, trafficQuery(window), window)
	if err != nil {
		return traffic{}, err
	}
	return parseTraffic(b, since, window, buckets)
}

func readStops(token, zone string, window time.Duration) ([]shieldEvent, error) {
	b, _, err := cfAsk(token, zone, stopsQuery, window)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			Viewer struct {
				Zones []struct {
					Events        []cfEvent `json:"events"`
					ShieldBlocked []struct {
						Dimensions cfEvent `json:"dimensions"`
					} `json:"shieldBlocked"`
				} `json:"zones"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if len(r.Errors) > 0 {
		return nil, errors.New(r.Errors[0].Message)
	}
	if len(r.Data.Viewer.Zones) == 0 {
		return nil, errors.New("the token can't see CF_ZONE_ID")
	}
	z := r.Data.Viewer.Zones[0]
	var out []shieldEvent
	for _, ev := range z.Events {
		rule := ev.Description
		if rule == "" {
			rule = ev.RuleID
		}
		asn := ev.ASN
		if ev.ASNName != "" {
			asn += " " + ev.ASNName
		}
		path := ev.Path
		if ev.Query != "" {
			path += "?" + strings.TrimPrefix(ev.Query, "?")
		}
		out = append(out, shieldEvent{
			at: ev.Datetime, source: ev.Source, what: clean(shield(ev.Source, ev.Action)), rule: clean(rule), ray: clean(ev.Ray),
			ip: clean(ev.IP), asn: clean(asn), country: clean(ev.Country), method: clean(ev.Method), host: clean(ev.Host), path: clean(path), agent: clean(ev.UserAgent),
		})
	}
	for _, g := range z.ShieldBlocked {
		ev := g.Dimensions
		out = append(out, shieldEvent{
			at: ev.Datetime, source: "shield", what: shield("shield", "block"), rule: "Shield: hosting networks",
			ip: clean(ev.IP), country: clean(ev.Country), method: clean(ev.Method), host: clean(ev.Host), path: clean(ev.Path), agent: clean(ev.UserAgent),
		})
	}
	var since time.Time
	if n := len(z.Events); n == stopsLimit {
		since = z.Events[n-1].Datetime
	}
	if n := len(z.ShieldBlocked); n == stopsLimit && z.ShieldBlocked[n-1].Dimensions.Datetime.After(since) {
		since = z.ShieldBlocked[n-1].Dimensions.Datetime
	}
	slices.SortStableFunc(out, func(a, b shieldEvent) int { return b.at.Compare(a.at) })
	return slices.DeleteFunc(out, func(e shieldEvent) bool { return e.at.Before(since) }), nil
}

type cfGroup struct {
	Count int `json:"count"`
	Sum   struct {
		Requests       int    `json:"requests"`
		Visits         int    `json:"visits"`
		PageViews      int    `json:"pageViews"`
		Bytes          uint64 `json:"bytes"`
		CachedBytes    uint64 `json:"cachedBytes"`
		CachedRequests int    `json:"cachedRequests"`
	} `json:"sum"`
	Ratio struct {
		Status4xx float64 `json:"status4xx"`
		Status5xx float64 `json:"status5xx"`
	} `json:"ratio"`
	Quantiles struct {
		P50 float64 `json:"p50"`
		P95 float64 `json:"p95"`
	} `json:"quantiles"`
	Dimensions struct {
		T      time.Time `json:"t"`
		Label  any       `json:"label"`
		Action string    `json:"action"`
		Source string    `json:"source"`
	} `json:"dimensions"`
}

type cfEvent struct {
	Datetime    time.Time `json:"datetime"`
	Action      string    `json:"action"`
	Source      string    `json:"source"`
	Description string    `json:"description"`
	RuleID      string    `json:"ruleId"`
	Ray         string    `json:"rayName"`
	IP          string    `json:"clientIP"`
	ASN         string    `json:"clientAsn"`
	ASNName     string    `json:"clientASNDescription"`
	Country     string    `json:"clientCountryName"`
	Method      string    `json:"clientRequestHTTPMethodName"`
	Host        string    `json:"clientRequestHTTPHost"`
	Path        string    `json:"clientRequestPath"`
	Query       string    `json:"clientRequestQuery"`
	UserAgent   string    `json:"userAgent"`
}

func parseTraffic(b []byte, since time.Time, window time.Duration, buckets int) (traffic, error) {
	var r struct {
		Data struct {
			Viewer struct {
				Zones []struct {
					Overview      []cfGroup `json:"overview"`
					ShieldBlocked []cfGroup `json:"shieldBlocked"`
					Total         []cfGroup `json:"total"`
					Origin        []cfGroup `json:"origin"`
					Hist          []cfGroup `json:"hist"`
					HistStopped   []cfGroup `json:"histStopped"`
					Fetched       []cfGroup `json:"fetched"`
					Countries     []cfGroup `json:"countries"`
					Paths         []cfGroup `json:"paths"`
					Statuses      []cfGroup `json:"statuses"`
					Cache         []cfGroup `json:"cache"`
					Security      []cfGroup `json:"security"`
					Bots          []cfGroup `json:"bots"`
				} `json:"zones"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return traffic{}, err
	}
	if len(r.Errors) > 0 {
		return traffic{}, errors.New(r.Errors[0].Message)
	}
	if len(r.Data.Viewer.Zones) == 0 {
		return traffic{}, errors.New("the token can't see CF_ZONE_ID")
	}
	z := r.Data.Viewer.Zones[0]
	e := traffic{
		hist:        bucket(z.Hist, since, window, buckets),
		histStopped: bucket(z.HistStopped, since, window, buckets),
		countries:   labelled(z.Countries),
		paths:       labelled(z.Paths),
		statuses:    statuses(z.Statuses),
		cache:       labelled(z.Cache),
		bots:        labelled(z.Bots),
	}
	if len(z.Fetched) > 0 {
		e.lastServed = z.Fetched[0].Dimensions.T
	}
	if len(z.Overview) > 0 {
		s := z.Overview[0].Sum
		e.requests, e.visits, e.pageViews = s.Requests, s.Visits, s.PageViews
		e.bytes, e.cachedBytes, e.cachedReqs = s.Bytes, s.CachedBytes, s.CachedRequests
	}
	if len(z.Total) > 0 {
		e.status4xx, e.status5xx = z.Total[0].Ratio.Status4xx, z.Total[0].Ratio.Status5xx
	}
	if len(z.Origin) > 0 {
		e.originP50, e.originP95 = z.Origin[0].Quantiles.P50, z.Origin[0].Quantiles.P95
	}
	for _, g := range z.HistStopped {
		e.stopped += g.Count
	}
	e.requests -= e.stopped
	for _, g := range z.Security {
		e.security = append(e.security, count{label: shield(g.Dimensions.Source, g.Dimensions.Action), n: g.Count})
	}
	if len(z.ShieldBlocked) > 0 && z.ShieldBlocked[0].Count > 0 {
		e.security = append(e.security, count{label: shield("shield", "block"), n: z.ShieldBlocked[0].Count})
		slices.SortStableFunc(e.security, func(a, b count) int { return b.n - a.n })
		e.security = e.security[:min(len(e.security), 5)]
	}
	return e, nil
}

func bucket(gs []cfGroup, since time.Time, window time.Duration, buckets int) []float64 {
	out := make([]float64, buckets)
	for _, g := range gs {
		i := int(float64(g.Dimensions.T.Sub(since)) / float64(window) * float64(buckets))
		out[clamp(i, 0, buckets-1)] += float64(g.Count)
	}
	return out
}

func labelled(gs []cfGroup) []count {
	out := make([]count, 0, len(gs))
	for _, g := range gs {
		out = append(out, count{label: clean(fmt.Sprint(g.Dimensions.Label)), n: g.Count})
	}
	return out
}

func statuses(gs []cfGroup) []count {
	out := labelled(gs)
	for i := range out {
		if code, err := strconv.Atoi(out[i].label); err == nil && http.StatusText(code) != "" {
			out[i].label += " " + http.StatusText(code)
		}
	}
	return out
}

var shieldSources = map[string]string{
	"shield":          "shield",
	"botFight":        "Bot Fight",
	"linkMaze":        "AI Labyrinth",
	"firewallCustom":  "custom rule",
	"firewallManaged": "managed rule",
	"bic":             "browser check",
	"securityLevel":   "security level",
	"ratelimit":       "rate limit",
	"hot":             "hotlink",
}

var shieldActions = map[string]string{
	"block":              "blocked",
	"managed_challenge":  "challenged",
	"challenge":          "challenged",
	"jschallenge":        "challenged",
	"link_maze_injected": "trap laid",
	"link_maze_visited":  "trap walked",
	"log":                "logged",
	"skip":               "skipped",
	"allow":              "allowed",
}

func shield(source, action string) string {
	s, ok := shieldSources[source]
	if !ok {
		s = source
	}
	a, ok := shieldActions[action]
	if !ok {
		a = action
	}
	return s + " " + a
}

func readZone(token, zone string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var z struct {
		Name string `json:"name"`
	}
	err := cfCall(ctx, token, http.MethodGet, "/zones/"+zone, nil, &z)
	return z.Name, err
}
