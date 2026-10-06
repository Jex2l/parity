package capture

import (
	"math/rand"
	"sync"
)

// SampleMeta carries context a Sampler may use to decide.
type SampleMeta struct {
	Status      int
	DurationMs  float64
	IsError     bool
	ParamShapeKey string // coarse fingerprint of query/body shape, for rarity detection
}

// Decision is the result of a sampling decision: whether to keep the
// exchange, and the statistical weight (1/effective_rate) to attach so
// aggregate stats computed later can be reweighted correctly.
type Decision struct {
	Keep   bool
	Weight float64
}

// Sampler decides whether to keep a given exchange.
type Sampler interface {
	Decide(endpointTemplate string, meta SampleMeta) Decision
}

// Config configures the default composite Sampler.
type Config struct {
	DefaultRate  float64            // e.g. 0.01
	PerEndpoint  map[string]float64 // endpoint template -> rate override (0 = never)
	Interesting  bool               // always keep rare statuses/errors/slow requests regardless of rate
	SlowMs       float64            // threshold for "slow" when Interesting is on
	ReservoirCap int                // per-endpoint reservoir size for rarity tracking; 0 disables
}

// CompositeSampler implements uniform/per-endpoint rate sampling plus an
// "interesting" override and a simple per-endpoint reservoir used to
// detect previously-unseen parameter shapes.
type CompositeSampler struct {
	cfg Config
	mu  sync.Mutex
	seenShapes map[string]map[string]bool // endpoint -> set of shape keys seen
	rng *rand.Rand
}

// NewCompositeSampler builds a Sampler from cfg.
func NewCompositeSampler(cfg Config) *CompositeSampler {
	if cfg.SlowMs == 0 {
		cfg.SlowMs = 1000
	}
	return &CompositeSampler{
		cfg:        cfg,
		seenShapes: map[string]map[string]bool{},
		rng:        rand.New(rand.NewSource(1)),
	}
}

func (s *CompositeSampler) rateFor(endpoint string) float64 {
	if r, ok := s.cfg.PerEndpoint[endpoint]; ok {
		return r
	}
	if s.cfg.DefaultRate == 0 {
		return 1.0
	}
	return s.cfg.DefaultRate
}

func (s *CompositeSampler) Decide(endpoint string, meta SampleMeta) Decision {
	rate := s.rateFor(endpoint)

	if s.cfg.Interesting {
		if meta.IsError || meta.Status >= 500 || meta.Status == 429 || meta.DurationMs >= s.cfg.SlowMs {
			return Decision{Keep: true, Weight: 1} // always-interesting records are not reweighted as if sampled
		}
		if meta.ParamShapeKey != "" && s.isNovelShape(endpoint, meta.ParamShapeKey) {
			return Decision{Keep: true, Weight: 1}
		}
	}

	if rate <= 0 {
		return Decision{Keep: false, Weight: 0}
	}
	if rate >= 1 {
		return Decision{Keep: true, Weight: 1}
	}
	if s.rng.Float64() < rate {
		return Decision{Keep: true, Weight: 1 / rate}
	}
	return Decision{Keep: false, Weight: 0}
}

func (s *CompositeSampler) isNovelShape(endpoint, shapeKey string) bool {
	if s.cfg.ReservoirCap == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set, ok := s.seenShapes[endpoint]
	if !ok {
		set = map[string]bool{}
		s.seenShapes[endpoint] = set
	}
	if set[shapeKey] {
		return false
	}
	if len(set) >= s.cfg.ReservoirCap {
		return false // reservoir full; stop flagging new shapes as novel
	}
	set[shapeKey] = true
	return true
}
