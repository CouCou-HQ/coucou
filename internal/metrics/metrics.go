// Package metrics declares everything coucou exports to Prometheus. Counters and histograms are
// package vars because that is what makes a call site one line at the point the thing happens;
// gauges that mirror live state are registered by Observe instead, so nothing has to remember to
// update them.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace    = "coucou"
	subEvents    = "events"
	labelOutcome = "outcome"
	labelHandler = "handler"
	labelTrigger = "trigger"
)

var (
	// PlaysTotal counts finished attempts. Outcome is "ok" or the reason it was not: "empty" when
	// everyone left, "<stage>_fail" when a stage broke. A play that never started because the guild
	// was busy is not counted — nothing happened.
	PlaysTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "plays_total",
		Help: "Finished play attempts by trigger and outcome.",
	}, []string{labelTrigger, labelOutcome})

	// PlayDuration is join to leave, so it includes the suspense pause. Buckets run 0.5s to 64s:
	// a play is a few seconds of audio plus up to 20s of suspense, and the 90s handler timeout is
	// the ceiling.
	PlayDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "play_duration_seconds",
		Help:    "Time from join to leave, including suspense.",
		Buckets: prometheus.ExponentialBuckets(0.5, 2, 8),
	}, []string{labelTrigger})

	// VoiceActive is how many plays hold a slot right now, against the eight the semaphore allows.
	// Sitting at the limit means the bus is waiting for a slot rather than the bot being idle.
	VoiceActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Name: "voice_active",
		Help: "Plays currently running, out of the concurrency limit.",
	})

	// BusHandled and BusHandleDuration are recorded by the router middleware, so they cover every
	// consumer without each one having to say so.
	BusHandled = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "bus_handled_total",
		Help: "Bus deliveries by handler and outcome.",
	}, []string{labelHandler, labelOutcome})

	BusHandleDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "bus_handle_duration_seconds",
		Help:    "Time a handler took. The voice worker returns as soon as it has a slot, so its play is not in here.",
		Buckets: prometheus.DefBuckets,
	}, []string{labelHandler})
)

// The gauges below mirror live state rather than counting events, so each takes the function that
// reads it. Register each once, at startup. They are called at scrape time, so none of them may
// block or touch the database.
//
// One registration per gauge rather than one call taking them all: guilds and sounds are both
// func() int, and a single argument list would let them be swapped silently.

// ObserveGuilds registers the count of guilds the bot is in.
func ObserveGuilds(n func() int) {
	promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: namespace, Name: "guilds",
		Help: "Guilds with a settings row, which is every guild the bot is in.",
	}, func() float64 { return float64(n()) })
}

// ObserveSounds registers the count of sound files the registry has found.
func ObserveSounds(n func() int) {
	promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: namespace, Name: "sounds_loaded",
		Help: "Sound files the registry has found.",
	}, func() float64 { return float64(n()) })
}

// ObserveBuffered registers the flush backlog, which is the pair to alert on: it only grows when
// writes are failing, and past maxBuffer the log drops the oldest rows silently. These gauges are
// the only warning that stats are being lost.
func ObserveBuffered(n func() (plays, misc int)) {
	promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: namespace, Subsystem: subEvents, Name: "buffered_plays",
		Help: "Play rows waiting to be flushed. Growing means writes are failing.",
	}, func() float64 { p, _ := n(); return float64(p) })

	promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: namespace, Subsystem: subEvents, Name: "buffered_misc",
		Help: "Event rows waiting to be flushed. Growing means writes are failing.",
	}, func() float64 { _, m := n(); return float64(m) })
}

// Handler serves the default registry, which promauto registered everything above into — plus the
// Go runtime and process collectors that come with it.
func Handler() http.Handler { return promhttp.Handler() }

// ObserveGatewayLatency registers the gateway heartbeat round trip, in seconds.
//
// Zero is the alert. disgo derives the latency as received-minus-sent, so a gateway that stopped
// answering reads as zero or negative rather than as a large duration — a bot whose gateway is
// dead otherwise looks perfectly healthy here, which is the gap this closes.
func ObserveGatewayLatency(d func() time.Duration) {
	promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: namespace, Name: "gateway_latency_seconds",
		Help: "Heartbeat round trip to the Discord gateway, worst shard. 0 means a shard is not answering.",
	}, func() float64 { return d().Seconds() })
}
