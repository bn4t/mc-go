package server

import (
	"sort"
	"testing"
	"time"
)

func benchmarkJoinPreparationSession(s *Server) *Session {
	sess := &Session{server: s, done: make(chan struct{})}
	sess.counted = true
	sess.state.Store(StateConfiguration)
	return sess
}

func benchmarkJoinPreparationJob(s *Server, sess *Session, radius int) *joinPreparationJob {
	d := s.dims[dimOverworld]
	return &joinPreparationJob{session: sess, plan: buildJoinPreparationPlan(joinPreparationTarget{
		dimension: d, radius: radius, skylight: d.skylight, incarnation: sess.ID(),
	})}
}

func runBenchmarkJoinPreparation(b *testing.B, s *Server, job *joinPreparationJob, record func(joinPreparationStep, time.Duration, time.Duration)) int {
	b.Helper()
	turns := 0
	for {
		var kind joinPreparationStep
		var complete bool
		var err error
		var work time.Duration
		start := time.Now()
		s.simulationAction(func() {
			workStart := time.Now()
			kind, complete, err = s.stepJoinPreparationOwned(job)
			work = time.Since(workStart)
		})
		elapsed := time.Since(start)
		turns++
		if record != nil {
			record(kind, work, elapsed)
		}
		if err != nil {
			b.Fatal(err)
		}
		if complete {
			return turns
		}
	}
}

func reportJoinPreparationDurations(b *testing.B, name string, samples []time.Duration) {
	if len(samples) == 0 {
		return
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	b.ReportMetric(float64(samples[(len(samples)-1)/2]), name+"-p50-ns")
	b.ReportMetric(float64(samples[(95*len(samples)+99)/100-1]), name+"-p95-ns")
	b.ReportMetric(float64(samples[len(samples)-1]), name+"-max-ns")
	b.ReportMetric(float64(len(samples))/float64(b.N), name+"-samples/op")
}

// Work samples time only stepJoinPreparationOwned after ownership is acquired.
// Turn samples include uncontended acquisition, work, deferred actions and stream
// flushes. The fixture has no pending deferred actions, sockets, ticks or competing
// owner requests; turn samples are not a contended owner-hold or tick bound.
// Observer latency during real joins is measured separately by the public TCP
// probe. Whole light floods are deliberately indivisible.
func BenchmarkJoinPreparationQuantum(b *testing.B) {
	for _, cold := range []bool{true, false} {
		name := "Warm"
		if cold {
			name = "Cold"
		}
		b.Run(name, func(b *testing.B) {
			s, w := columnPreparationFixture(b)
			sess := benchmarkJoinPreparationSession(s)
			resetColumnPreparation(s, w)
			runBenchmarkJoinPreparation(b, s, benchmarkJoinPreparationJob(s, sess, 2), nil)
			facts := make([]time.Duration, 0, b.N*49)
			factsWork := make([]time.Duration, 0, b.N*49)
			light := make([]time.Duration, 0, b.N*121)
			lightWork := make([]time.Duration, 0, b.N*121)
			skips := make([]time.Duration, 0, b.N*3)
			skipsWork := make([]time.Duration, 0, b.N*3)
			turns := 0
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if cold {
					resetColumnPreparation(s, w)
				}
				job := benchmarkJoinPreparationJob(s, sess, 2)
				b.StartTimer()
				turns += runBenchmarkJoinPreparation(b, s, job, func(kind joinPreparationStep, work, elapsed time.Duration) {
					switch kind {
					case joinPreparationFacts:
						facts = append(facts, elapsed)
						factsWork = append(factsWork, work)
					case joinPreparationLight:
						light = append(light, elapsed)
						lightWork = append(lightWork, work)
					default:
						skips = append(skips, elapsed)
						skipsWork = append(skipsWork, work)
					}
				})
			}
			b.StopTimer()
			b.ReportMetric(float64(turns)/float64(b.N), "owned-turns/op")
			reportJoinPreparationDurations(b, "facts-turn", facts)
			reportJoinPreparationDurations(b, "facts-work", factsWork)
			reportJoinPreparationDurations(b, "light-turn", light)
			reportJoinPreparationDurations(b, "light-work", lightWork)
			reportJoinPreparationDurations(b, "skip-turn", skips)
			reportJoinPreparationDurations(b, "skip-work", skipsWork)
		})
	}
}

// This measures the actual reservation, submission, worker turns and completion
// of an already warm radius-2 plan. Plan building is a separate operation; the
// immutable coordinate slices can be reused after a completed job.
func BenchmarkJoinPreparationWarmAdmission(b *testing.B) {
	s, w := columnPreparationFixture(b)
	sess := benchmarkJoinPreparationSession(s)
	resetColumnPreparation(s, w)
	plan := benchmarkJoinPreparationJob(s, sess, 2).plan
	runBenchmarkJoinPreparation(b, s, &joinPreparationJob{session: sess, plan: plan}, nil)
	scheduler := newJoinPreparationScheduler(s, maxJoinPreparationJobs)
	b.Cleanup(scheduler.close)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job, ok := scheduler.reserve(sess)
		if !ok || !scheduler.submit(job, plan) {
			b.Fatal("idle scheduler refused warm plan")
		}
		<-job.done
		if job.err != nil {
			b.Fatal(job.err)
		}
	}
}

func BenchmarkJoinPreparationPlan(b *testing.B) {
	for _, radius := range []int{2, maxJoinPreparationRadius} {
		name := "Radius2"
		if radius == maxJoinPreparationRadius {
			name = "Radius32"
		}
		b.Run(name, func(b *testing.B) {
			target := joinPreparationTarget{radius: radius, skylight: true}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				plan := buildJoinPreparationPlan(target)
				if len(plan.facts) == 0 || len(plan.light) == 0 {
					b.Fatal("empty preparation plan")
				}
			}
		})
	}
}
