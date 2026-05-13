package core

// RuleStatus is the explicit per-rule outcome.
//
// v9.3.2: every check rule MUST emit one of these three states. Pre-v9.3.2
// the model was "no issue returned == pass", which conflated "rule ran and
// found nothing wrong" with "rule didn't run at all" — the latter happens
// when a code path skips a check (HTML carve-out, hook parse-error,
// unhandled lang switch case, …) and silently grants pass. v9.3.2 forces
// every rule to register a positive signal; aggregators treat an
// unregistered rule as Fail.
type RuleStatus int

const (
	// StatusUnknown is the zero value. A rule observed as Unknown by the
	// aggregator means "no signal was recorded for this rule" — treated as
	// Fail. Never emitted explicitly; only present as a default.
	StatusUnknown RuleStatus = iota

	// StatusPass — the rule ran and the object satisfied it.
	StatusPass

	// StatusFail — the rule ran and the object did NOT satisfy it. The
	// associated StaticIssue(s) carry the failure detail.
	StatusFail

	// StatusSkipped — the rule was deliberately not run (e.g. doesn't
	// apply to this object's branch, or its prerequisite tool didn't
	// run). The Reason field of RuleRun carries why.
	StatusSkipped
)

func (s RuleStatus) String() string {
	switch s {
	case StatusPass:
		return "pass"
	case StatusFail:
		return "fail"
	case StatusSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// RuleRun records one rule's outcome. A check returning a slice of
// RuleRun (or wrapping it in CheckReport) gives the aggregator the
// information it needs to detect "rule didn't fire" — which under v9.3.2
// is always a bug, never an implicit pass.
type RuleRun struct {
	Code   string       // rule identifier (e.g. "effects-empty", "runtime-trace-missing")
	Status RuleStatus
	Issues []StaticIssue // populated when Status == StatusFail
	Reason string        // populated when Status == StatusSkipped
}

// CheckReport is the structured return value of a check (StaticCheck,
// RuntimeCheck). Carries all rule outcomes; downstream aggregators
// decide pass/fail.
type CheckReport struct {
	Runs []RuleRun
}

// Issues returns the union of Fail issues across every rule run.
// Maintains backward compatibility with callers that just want the
// human-readable issue list.
func (r CheckReport) Issues() []StaticIssue {
	var out []StaticIssue
	for _, run := range r.Runs {
		if run.Status == StatusFail {
			out = append(out, run.Issues...)
		}
	}
	return out
}

// Coverage returns the rule-code → status map. Used by aggregators that
// need to detect "rule expected but didn't run".
func (r CheckReport) Coverage() map[string]RuleStatus {
	out := make(map[string]RuleStatus, len(r.Runs))
	for _, run := range r.Runs {
		out[run.Code] = run.Status
	}
	return out
}

// ReportBuilder is the helper checks use to emit per-rule outcomes
// without ceremony. Each rule's emission site calls one of pass / fail /
// skip on the same builder; build collects them into a CheckReport.
//
// The builder enforces "one rule, one emission" by preserving the FIRST
// emit per code — a rule that tries to register pass and then fail is a
// programmer error (the rule body branched inconsistently), surfaced as
// a noop on the second call. Callers should structure rules so each
// terminates in exactly one of pass / fail / skip.
type ReportBuilder struct {
	runs  []RuleRun
	codes map[string]bool
}

// NewReportBuilder creates a fresh report builder.
func NewReportBuilder() *ReportBuilder {
	return &ReportBuilder{codes: map[string]bool{}}
}

// Pass records a successful rule run.
func (b *ReportBuilder) Pass(code string) {
	if b.codes[code] {
		return
	}
	b.codes[code] = true
	b.runs = append(b.runs, RuleRun{Code: code, Status: StatusPass})
}

// Fail records a failing rule run, with one or more StaticIssues
// describing what's wrong. Issues must already have Code matching the
// rule code.
func (b *ReportBuilder) Fail(code string, issues ...StaticIssue) {
	if b.codes[code] {
		return
	}
	b.codes[code] = true
	b.runs = append(b.runs, RuleRun{Code: code, Status: StatusFail, Issues: issues})
}

// Skip records that the rule was deliberately not run, with a
// machine-readable reason. The aggregator treats Skip as acceptable
// for the rule (does NOT fail overall), but the reason is visible in
// rendered output so the agent can audit "why didn't this check run".
func (b *ReportBuilder) Skip(code, reason string) {
	if b.codes[code] {
		return
	}
	b.codes[code] = true
	b.runs = append(b.runs, RuleRun{Code: code, Status: StatusSkipped, Reason: reason})
}

// Build returns the assembled CheckReport.
func (b *ReportBuilder) Build() CheckReport {
	return CheckReport{Runs: append([]RuleRun(nil), b.runs...)}
}

// AggregateOK is the canonical v9.3.2 aggregator decision: every
// expected rule must have an explicit Pass or Skipped status; ANY rule
// in StatusUnknown or StatusFail causes overall failure.
//
// expected is the set of rule codes the caller knows should have run
// for this object's branch. Rules in the report that aren't in expected
// are tolerated (e.g. defensive extra signals); rules in expected that
// aren't in the report return false (unregistered rule = bug, fail-closed).
func AggregateOK(report CheckReport, expected []string) (ok bool, missing []string, failed []string) {
	cov := report.Coverage()
	ok = true
	for _, code := range expected {
		s, present := cov[code]
		if !present {
			missing = append(missing, code)
			ok = false
			continue
		}
		switch s {
		case StatusFail:
			failed = append(failed, code)
			ok = false
		case StatusUnknown:
			missing = append(missing, code)
			ok = false
		}
	}
	return ok, missing, failed
}
