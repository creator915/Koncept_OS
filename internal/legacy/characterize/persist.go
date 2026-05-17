package characterize

import (
	"encoding/json"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// ToSection maps a rich CharResult onto the core-persistable
// CharacterizationSection. The full result is kept losslessly in
// Detail (设计文档 Part 10.3/10.4: the client can audit the lock and
// the agent's assumption path WITHOUT the agent). The promoted scalar
// fields are what the gate's Method-Use-Rule needs without parsing
// Detail.
func ToSection(res *CharResult) *core.CharacterizationSection {
	if res == nil {
		return nil
	}
	detail, _ := json.Marshal(res)
	cond := make([]string, 0, len(res.Assumptions))
	for _, a := range res.Assumptions {
		cond = append(cond, a.Statement)
	}
	return &core.CharacterizationSection{
		SuiteID:          res.Reproducible.SuiteID,
		ImplSymbol:       res.Lock.ImplSymbol,
		Lang:             res.Lock.Lang,
		ArtifactRef:      res.Lock.ArtifactRef,
		CodeHash:         res.Lock.CodeHash,
		Cases:            res.Lock.Cases,
		LockedCount:      len(res.Lock.Cases),
		UnlockedCount:    len(res.Lock.Unlocked),
		Unlocked:         res.Lock.Unlocked,
		OracleProperty:   res.Oracle.Property,
		ConfidenceReport: res.Oracle.Confidence.Report(),
		ConditionalOn:    cond,
		Detail:           detail,
		Timestamp:        res.Lock.CreatedAt,
	}
}

// Persist writes the characterization lock into objectID's evidence
// bundle as an additive section. Greenfield objects are never touched
// (only the characterize front stage calls this).
func Persist(objectID string, res *CharResult) error {
	return core.WriteCharacterization(objectID, ToSection(res))
}
