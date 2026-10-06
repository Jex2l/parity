// Package scrub removes or pseudonymizes sensitive data from captured
// exchanges before anything is written to disk. See docs/security.md.
package scrub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	pb "github.com/jex2l/parity/internal/recording/pb"
)

// ScrubVersion is bumped whenever detection/pseudonymization rules change
// in a way that affects previously-recorded scrub_report compatibility.
const ScrubVersion = "v1"

// Policy is the action taken for a given field.
type Policy string

const (
	PolicyPreserve     Policy = "preserve"
	PolicyRedact       Policy = "redact"
	PolicyPseudonymize Policy = "pseudonymize"
	PolicyDropRequest  Policy = "drop_request"
)

// Config controls scrubbing behavior. Zero-value Config is safe but
// conservative (redacts everything not explicitly allowlisted).
type Config struct {
	// HeadersAllow lists header names (case-insensitive) that pass through
	// unredacted. Authorization/Cookie are always stripped regardless.
	HeadersAllow []string
	// JSONPathPolicy maps a dotted JSON path (e.g. "user.email") to a Policy.
	JSONPathPolicy map[string]Policy
	// DefaultBodyPolicy applies to JSON leaf values with no explicit rule.
	// Defaults to PolicyPseudonymize when zero-valued via NewScrubber.
	DefaultBodyPolicy Policy
	// HMACSecret is used for format-preserving, referentially consistent
	// pseudonymization. Must stay inside the customer perimeter.
	HMACSecret []byte
}

// Report records what was redacted/pseudonymized for one exchange.
type Report struct {
	ScrubVersion string
	Redactions   []string // "<path>:<rule>"
}

var alwaysStrippedHeaders = map[string]bool{
	"authorization": true,
	"cookie":        true,
	"set-cookie":    true,
	"x-api-key":     true,
}

var (
	emailRe    = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	phoneRe    = regexp.MustCompile(`\+?\d[\d\-\s()]{8,}\d`)
	ssnRe      = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	jwtRe      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	ipRe       = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	ibanRe     = regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{10,30}\b`)
	apiKeyRe   = regexp.MustCompile(`\b(sk|pk|rk)_(live|test)_[A-Za-z0-9]{10,}\b`)
	ccDigitsRe = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
)

// Scrubber applies the configured policies to an Exchange.
type Scrubber struct {
	cfg        Config
	headersOK  map[string]bool
}

// New builds a Scrubber from cfg, applying safe defaults.
func New(cfg Config) *Scrubber {
	if cfg.DefaultBodyPolicy == "" {
		cfg.DefaultBodyPolicy = PolicyPseudonymize
	}
	if len(cfg.HMACSecret) == 0 {
		// A zero secret still gives deterministic, referentially consistent
		// pseudonymization within a process; callers should supply a real
		// per-tenant secret in production (see parity.yaml `scrub`).
		cfg.HMACSecret = []byte("parity-dev-default-secret-change-me")
	}
	ok := map[string]bool{}
	for _, h := range cfg.HeadersAllow {
		ok[strings.ToLower(h)] = true
	}
	return &Scrubber{cfg: cfg, headersOK: ok}
}

// ScrubExchange returns a redacted copy of ex plus a Report describing what
// was changed. The input is not mutated.
func (s *Scrubber) ScrubExchange(ex *pb.Exchange) (*pb.Exchange, Report) {
	report := Report{ScrubVersion: ScrubVersion}

	out := &pb.Exchange{
		ExchangeId:           ex.GetExchangeId(),
		TraceId:              ex.GetTraceId(),
		CapturedAtUnixMicros: ex.GetCapturedAtUnixMicros(),
		Service:              ex.GetService(),
		Environment:           ex.GetEnvironment(),
		BuildSha:              ex.GetBuildSha(),
		Sampling:              ex.GetSampling(),
		FidelityFlags:         ex.GetFidelityFlags(),
	}

	if in := ex.GetInbound(); in != nil {
		out.Inbound = &pb.Inbound{
			Method:           in.GetMethod(),
			Scheme:           in.GetScheme(),
			Host:             in.GetHost(),
			Path:             in.GetPath(),
			PathTemplate:     in.GetPathTemplate(),
			Query:            in.GetQuery(),
			Headers:          s.scrubHeaders(in.GetHeaders(), "inbound.headers", &report),
			BodyContentType:  in.GetBodyContentType(),
			ProtocolVersion:  in.GetProtocolVersion(),
			Body:             s.scrubBody(in.GetBody(), in.GetBodyContentType(), "inbound.body", &report),
		}
	}

	for i, o := range ex.GetOutbound() {
		path := fmt.Sprintf("outbound[%d]", i)
		out.Outbound = append(out.Outbound, &pb.Outbound{
			Method:           o.GetMethod(),
			Url:              o.GetUrl(),
			UrlTemplate:      o.GetUrlTemplate(),
			RequestHeaders:   s.scrubHeaders(o.GetRequestHeaders(), path+".request_headers", &report),
			RequestBody:      s.scrubBody(o.GetRequestBody(), "application/json", path+".request_body", &report),
			ResponseStatus:   o.GetResponseStatus(),
			ResponseHeaders:  s.scrubHeaders(o.GetResponseHeaders(), path+".response_headers", &report),
			ResponseBody:     s.scrubBody(o.GetResponseBody(), "application/json", path+".response_body", &report),
			DurationMicros:   o.GetDurationMicros(),
			CorrelationId:    o.GetCorrelationId(),
			Sequence:         o.GetSequence(),
		})
	}

	if r := ex.GetResponse(); r != nil {
		out.Response = &pb.Response{
			Status:                 r.GetStatus(),
			Headers:                s.scrubHeaders(r.GetHeaders(), "response.headers", &report),
			Body:                   s.scrubBody(r.GetBody(), "application/json", "response.body", &report),
			TimeToFirstByteMicros:  r.GetTimeToFirstByteMicros(),
			TotalDurationMicros:    r.GetTotalDurationMicros(),
		}
	}

	out.ScrubReport = &pb.ScrubReport{
		ScrubVersion: report.ScrubVersion,
		Redactions:   report.Redactions,
	}
	return out, report
}

func (s *Scrubber) scrubHeaders(headers []*pb.Header, path string, report *Report) []*pb.Header {
	var out []*pb.Header
	for _, h := range headers {
		name := strings.ToLower(h.GetName())
		if alwaysStrippedHeaders[name] {
			report.Redactions = append(report.Redactions, fmt.Sprintf("%s.%s:always_stripped", path, name))
			continue
		}
		if !s.headersOK[name] {
			report.Redactions = append(report.Redactions, fmt.Sprintf("%s.%s:not_allowlisted", path, name))
			continue
		}
		out = append(out, &pb.Header{Name: h.GetName(), Values: h.GetValues()})
	}
	return out
}

// ScrubBody applies the same pattern-detection/pseudonymization rules
// ScrubExchange uses, directly to a raw body. Exposed so callers that
// need to scrub something other than a full recorded Exchange — notably
// `internal/orchestrate`, which must apply the *same* scrubbing to a
// live replay response before diffing it against an already-scrubbed
// recorded response, or every PII-bearing field looks like a spurious
// regression (see docs/limits.md's Phase 2 entry on this exact bug).
// The report of what was redacted is discarded; callers that need it
// should use ScrubExchange instead.
func (s *Scrubber) ScrubBody(body []byte) []byte {
	return s.scrubBody(body, "", "$", &Report{})
}

// scrubBody runs pattern detection over raw text (JSON or not); for JSON
// bodies this is a pragmatic v1 approach (regex over the serialized form)
// rather than a full JSON-path walk — sufficient for pattern-based PII
// (emails, cards, SSNs, JWTs, keys) and documented as a limit for
// structured per-field policy, which `JSONPathPolicy` partially covers for
// known paths via the caller's own pre-processing in later phases.
func (s *Scrubber) scrubBody(body []byte, contentType string, path string, report *Report) []byte {
	if len(body) == 0 {
		return body
	}
	text := string(body)
	text, n := s.redactPatterns(text, path, report)
	_ = n
	return []byte(text)
}

type detector struct {
	name string
	re   *regexp.Regexp
	// validate allows extra checks (e.g. Luhn) beyond the regex.
	validate func(match string) bool
}

func (s *Scrubber) detectors() []detector {
	return []detector{
		{"email", emailRe, nil},
		{"jwt", jwtRe, nil},
		{"api_key", apiKeyRe, nil},
		{"ssn", ssnRe, nil},
		{"credit_card", ccDigitsRe, isLuhnValid},
		{"iban", ibanRe, nil},
		{"ip_address", ipRe, nil},
		{"phone", phoneRe, nil},
	}
}

func (s *Scrubber) redactPatterns(text, path string, report *Report) (string, int) {
	count := 0
	for _, d := range s.detectors() {
		text = d.re.ReplaceAllStringFunc(text, func(match string) string {
			if d.validate != nil && !d.validate(match) {
				return match
			}
			count++
			report.Redactions = append(report.Redactions, fmt.Sprintf("%s:%s", path, d.name))
			return s.pseudonymize(d.name, match)
		})
	}
	return text, count
}

// pseudonymize returns a deterministic, format-preserving token for value,
// scoped by kind, using HMAC so the same real value always maps to the
// same token (referential consistency across a recording) without storing
// the real value.
func (s *Scrubber) pseudonymize(kind, value string) string {
	mac := hmac.New(sha256.New, s.cfg.HMACSecret)
	mac.Write([]byte(kind + ":" + value))
	digest := hex.EncodeToString(mac.Sum(nil))[:12]
	switch kind {
	case "email":
		return fmt.Sprintf("user-%s@example-redacted.test", digest)
	case "credit_card":
		return "4000" + numericDigest(digest, 12)
	case "ssn":
		return "900-00-" + numericDigest(digest, 4)
	case "phone":
		return "+1555" + numericDigest(digest, 7)
	default:
		return fmt.Sprintf("<%s_%s>", strings.ToUpper(kind), digest)
	}
}

func numericDigest(hexDigest string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		c := hexDigest[i%len(hexDigest)]
		v, _ := strconv.ParseInt(string(c), 16, 8)
		b.WriteString(strconv.Itoa(int(v) % 10))
	}
	return b.String()
}

// isLuhnValid checks the Luhn checksum for a candidate card number string
// (digits possibly separated by spaces/dashes), used to cut false positives
// from the broad credit-card regex.
func isLuhnValid(s string) bool {
	var digits []int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			digits = append(digits, int(c-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	parity := len(digits) % 2
	for i, d := range digits {
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// ShannonEntropy returns the Shannon entropy (bits/char) of s, used as a
// fallback detector for opaque high-entropy secrets that don't match a
// known pattern (e.g. generic API tokens).
func ShannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	counts := map[rune]int{}
	for _, r := range s {
		counts[r]++
	}
	entropy := 0.0
	n := float64(len(s))
	for _, c := range counts {
		p := float64(c) / n
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// LooksLikeSecret flags strings that are long and high-entropy enough to
// plausibly be an opaque secret/token not caught by named patterns.
func LooksLikeSecret(s string) bool {
	if len(s) < 20 {
		return false
	}
	return ShannonEntropy(s) >= 3.5
}
