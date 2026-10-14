package roles

import "strings"

// QAPairsBegin and QAPairsEnd fence the generated pairs table in the root
// CLAUDE.md so add-agent can re-render it without touching the rest of the
// file.
const (
	QAPairsBegin = "<!-- initech:qa-pairs (generated from the roles list; add-agent re-renders it unless you edit it) -->"
	QAPairsEnd   = "<!-- /initech:qa-pairs -->"
)

// QAPair is one QA agent and the engineers whose ready work it verifies.
type QAPair struct {
	QA        string
	Engineers []string
}

// QAPairs pairs the engineer roles in roles with the QA roles, in roster
// order. Engineers are split into contiguous blocks, one per QA, and the
// first QAs take the remainder (5 engineers, 2 QA: three to qa1, two to
// qa2). QA agents beyond the number of engineers get no pair. It returns nil
// when the roster has no engineer or no QA role. Family comes from
// RoleFamilyOf, so eng*/qa* names count and every other role is ignored.
func QAPairs(roles []string) []QAPair {
	var engs, qas []string
	for _, r := range roles {
		switch RoleFamilyOf(r) {
		case FamilyEng:
			engs = append(engs, r)
		case FamilyQA:
			qas = append(qas, r)
		}
	}
	if len(engs) == 0 || len(qas) == 0 {
		return nil
	}
	if len(qas) > len(engs) {
		qas = qas[:len(engs)]
	}
	base, extra := len(engs)/len(qas), len(engs)%len(qas)
	pairs := make([]QAPair, 0, len(qas))
	next := 0
	for i, qa := range qas {
		n := base
		if i < extra {
			n++
		}
		pairs = append(pairs, QAPair{QA: qa, Engineers: engs[next : next+n]})
		next += n
	}
	return pairs
}

// RenderQAPairsBlock renders the generated pairs table for a roster, fenced
// by QAPairsBegin/QAPairsEnd and ending in a newline. A roster with no QA
// role says who grades instead; one with no engineers says the table fills
// in as they are added. RenderRootCLAUDE puts it in place of
// {{qa_pairs_table}} in RootQAPairsSection.
func RenderQAPairsBlock(roles []string) string {
	var b strings.Builder
	b.WriteString(QAPairsBegin + "\n")
	pairs := QAPairs(roles)
	switch {
	case len(pairs) > 0:
		b.WriteString("| Engineers | QA |\n|-----------|----|\n")
		for _, p := range pairs {
			b.WriteString("| " + strings.Join(p.Engineers, ", ") + " | " + p.QA + " |\n")
		}
	case hasFamily(roles, FamilyEng):
		b.WriteString("There is no QA role: super grades or the operator adds one.\n")
	default:
		b.WriteString("No engineer roles yet; the table fills in as they are added.\n")
	}
	b.WriteString(QAPairsEnd + "\n")
	return b.String()
}

func hasFamily(roles []string, f RoleFamily) bool {
	for _, r := range roles {
		if RoleFamilyOf(r) == f {
			return true
		}
	}
	return false
}

// PairsResult says what ReplaceQAPairsBlock did.
type PairsResult int

const (
	// PairsUpdated: the block matched the old roster's render and was
	// replaced with the new roster's.
	PairsUpdated PairsResult = iota
	// PairsEdited: the block differs from the old roster's render (the
	// project edited it); the document is returned unchanged.
	PairsEdited
	// PairsMissing: no complete marker pair was found; the document is
	// returned unchanged.
	PairsMissing
	// PairsCurrent: the block already is the new roster's render (the file
	// was just generated with it); the document is returned unchanged.
	PairsCurrent
)

// ReplaceQAPairsBlock re-renders the fenced QA pairs block in doc for
// newRoles. It only replaces a block that is byte-for-byte the render of
// oldRoles, so a table the project edited by hand is never overwritten; in
// that case, when the markers are absent, and when the block already matches
// newRoles, doc is returned unchanged with the reason.
func ReplaceQAPairsBlock(doc string, oldRoles, newRoles []string) (string, PairsResult) {
	start := strings.Index(doc, QAPairsBegin)
	if start < 0 {
		return doc, PairsMissing
	}
	endRel := strings.Index(doc[start:], QAPairsEnd)
	if endRel < 0 {
		return doc, PairsMissing
	}
	end := start + endRel + len(QAPairsEnd)
	if end < len(doc) && doc[end] == '\n' {
		end++
	}
	switch doc[start:end] {
	case RenderQAPairsBlock(newRoles):
		return doc, PairsCurrent
	case RenderQAPairsBlock(oldRoles):
	default:
		return doc, PairsEdited
	}
	return doc[:start] + RenderQAPairsBlock(newRoles) + doc[end:], PairsUpdated
}
