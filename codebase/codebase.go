// Package codebase is the POC in-memory Codebase: immutable Goal artifacts,
// a mutable (Name, Arity) index with revisions, and the deduction ledger.
//
// Hashes use the POC profile "poc-sha256-canon/0" and are not claimed to be
// interoperable with any other Runtime.
package codebase

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/expr"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
)

// HashProfile names the artifact encoding used by this POC.
const HashProfile = "poc-sha256-canon/1"

// Artifact is one immutable stored Goal term.
type Artifact struct {
	Hash          string
	StructureHash string
	Name          string
	Arity         int
	Def           *sema.GoalDef
	Values        *expr.Unit
	// Pinned maps hash-qualified Name nodes in Def to full hashes.
	Pinned map[*syntax.Name]string
	// Canonical is the authored projection that was hashed.
	Canonical string
}

// GoalNodeID is the policy-erased structural identity plus arity.
func (a *Artifact) GoalNodeID() string { return fmt.Sprintf("%s/%d", a.StructureHash[:16], a.Arity) }

// Short returns an abbreviated hash for traces.
func (a *Artifact) Short() string { return a.Hash[:12] }

// DeductionRecord is one committed deduction.
type DeductionRecord struct {
	Seq              int      `json:"seq"`
	RunID            string   `json:"runId"`
	Occurrence       string   `json:"occurrence"`
	ReferenceKind    string   `json:"referenceKind"` // unqualified | hash-qualified | inline-arrow
	RequestedName    string   `json:"requestedName,omitempty"`
	RequestedArity   int      `json:"requestedArity"`
	ArtifactHash     string   `json:"artifactHash,omitempty"`
	GoalNodeID       string   `json:"goalNodeId,omitempty"`
	CodebaseRevision int      `json:"codebaseRevision"`
	Arguments        []string `json:"arguments"`
	Result           string   `json:"result"`
	Lineages         []string `json:"lineages"`
	PrimitiveProfile string   `json:"primitiveProfile"`
	Cause            string   `json:"cause"` // demand | prefetch
}

type nameArity struct {
	name  string
	arity int
}

// Codebase is safe for concurrent use.
type Codebase struct {
	mu        sync.Mutex
	artifacts map[string]*Artifact
	index     map[nameArity]string
	revision  int
	ledger    []DeductionRecord
	byOcc     map[ledgerKey]int
	runs      map[string]bool
	runSeq    int
}

// ledgerKey identifies a deduction: occurrence IDs are only unique within a run.
type ledgerKey struct {
	run, occurrence string
}

// New returns an empty Codebase.
func New() *Codebase {
	return &Codebase{artifacts: map[string]*Artifact{}, index: map[nameArity]string{},
		byOcc: map[ledgerKey]int{}, runs: map[string]bool{}}
}

// Revision returns the current codebase revision.
func (c *Codebase) Revision() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.revision
}

// Arities implements sema.Index.
func (c *Codebase) Arities(name string) []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []int
	for k := range c.index {
		if k.name == name {
			out = append(out, k.arity)
		}
	}
	sort.Ints(out)
	return out
}

// ResolvePrefix implements sema.Index.
func (c *Codebase) ResolvePrefix(name, prefix string) []sema.PrefixMatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resolvePrefixLocked(name, prefix)
}

func (c *Codebase) resolvePrefixLocked(name, prefix string) []sema.PrefixMatch {
	var out []sema.PrefixMatch
	for h, a := range c.artifacts {
		if a.Name == name && strings.HasPrefix(h, strings.ToLower(prefix)) {
			out = append(out, sema.PrefixMatch{Hash: h, Arity: a.Arity})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hash < out[j].Hash })
	return out
}

// Get returns an artifact by full hash.
func (c *Codebase) Get(hash string) (*Artifact, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.artifacts[hash]
	return a, ok
}

// ResolveCurrent atomically reads the current alias and revision.
func (c *Codebase) ResolveCurrent(name string, arity int) (*Artifact, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.index[nameArity{name, arity}]
	if !ok {
		return nil, c.revision, false
	}
	return c.artifacts[h], c.revision, true
}

// CommitUnit stores every Goal of a validated unit and moves their aliases in
// one revision. It returns the artifacts in definition order.
func (c *Codebase) CommitUnit(u *sema.Unit) ([]*Artifact, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var staged []*Artifact
	for _, def := range u.Goals {
		pinned := map[*syntax.Name]string{}
		var perr error
		resolve := func(n *syntax.Name) string {
			if full, ok := pinned[n]; ok {
				return full
			}
			m := c.resolvePrefixLocked(n.Ident, n.Hash)
			if len(m) != 1 {
				if perr == nil {
					kind := "HashNotFound"
					if len(m) > 1 {
						kind = "AmbiguousHashPrefix"
					}
					perr = diag.New(kind, diag.Validation, diag.At(n.Span), "%s#%s resolves to %d artifacts", n.Ident, n.Hash, len(m))
				}
				return n.Hash
			}
			pinned[n] = m[0].Hash
			return m[0].Hash
		}
		authored := canonicalDef(def, u.Values, false, resolve)
		structural := canonicalDef(def, u.Values, true, resolve)
		if perr != nil {
			return nil, c.revision, perr
		}
		staged = append(staged, &Artifact{
			Hash:          digest(authored),
			StructureHash: digest(structural),
			Name:          def.Name,
			Arity:         def.Arity,
			Def:           def,
			Values:        u.Values,
			Pinned:        pinned,
			Canonical:     authored,
		})
	}
	c.revision++
	for _, a := range staged {
		if existing, ok := c.artifacts[a.Hash]; ok {
			a = existing
		} else {
			c.artifacts[a.Hash] = a
		}
		c.index[nameArity{a.Name, a.Arity}] = a.Hash
	}
	return staged, c.revision, nil
}

// Rebind moves an alias to an existing artifact and returns the new revision.
func (c *Codebase) Rebind(name string, arity int, hash string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.artifacts[hash]
	if !ok || a.Name != name || a.Arity != arity {
		return c.revision, fmt.Errorf("artifact %s is not %s/%d", hash, name, arity)
	}
	c.revision++
	c.index[nameArity{name, arity}] = hash
	return c.revision, nil
}

// RegisterRun reserves a run identity. An empty id allocates a fresh one.
func (c *Codebase) RegisterRun(id string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" {
		for {
			c.runSeq++
			id = fmt.Sprintf("run-%d", c.runSeq)
			if !c.runs[id] {
				break
			}
		}
	}
	if c.runs[id] {
		return "", fmt.Errorf("run %s already exists in this codebase", id)
	}
	c.runs[id] = true
	return id, nil
}

// AppendDeduction commits a record. An occurrence commits at most once per run.
func (c *Codebase) AppendDeduction(r DeductionRecord) (DeductionRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.RunID == "" {
		return r, fmt.Errorf("deduction record for %s has no run identity", r.Occurrence)
	}
	key := ledgerKey{r.RunID, r.Occurrence}
	if _, dup := c.byOcc[key]; dup {
		return r, fmt.Errorf("occurrence %s already committed in run %s", r.Occurrence, r.RunID)
	}
	r.Seq = len(c.ledger) + 1
	c.byOcc[key] = len(c.ledger)
	c.ledger = append(c.ledger, r)
	return r, nil
}

// GetDeduction returns the committed record for an occurrence of a run.
func (c *Codebase) GetDeduction(runID, occ string) (DeductionRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i, ok := c.byOcc[ledgerKey{runID, occ}]
	if !ok {
		return DeductionRecord{}, false
	}
	return c.ledger[i], true
}

// Ledger returns a copy of all records of every run in commit order.
func (c *Codebase) Ledger() []DeductionRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]DeductionRecord(nil), c.ledger...)
}

// RunLedger returns the records of one run in commit order.
func (c *Codebase) RunLedger(runID string) []DeductionRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []DeductionRecord
	for _, r := range c.ledger {
		if r.RunID == runID {
			out = append(out, r)
		}
	}
	return out
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// canonicalDef hashes the definition together with the top-level values it
// can observe (all unit values, sorted), which keeps the POC encoding simple.
func canonicalDef(def *sema.GoalDef, values *expr.Unit, erase bool, resolve func(*syntax.Name) string) string {
	var b strings.Builder
	names := make([]string, 0, len(values.Values))
	for n := range values.Values {
		names = append(names, n)
	}
	sort.Strings(names)
	syntax.FrameOpen(&b, "artifact", 4+2*len(names))
	syntax.FrameAtom(&b, "p", HashProfile)
	// POC simplification: the authored projection includes the definition's
	// name so one stored artifact has one name; the structural projection
	// excludes it, so GoalNodeId is shared across names.
	if erase {
		syntax.FrameAtom(&b, "i", "")
	} else {
		syntax.FrameAtom(&b, "i", def.Name)
	}
	syntax.FrameAtom(&b, "a", fmt.Sprint(def.Arity))
	b.WriteString(syntax.Canonical(def.Arrow, erase, resolve))
	for _, n := range names {
		syntax.FrameAtom(&b, "v", n)
		b.WriteString(syntax.Canonical(values.Values[n], erase, resolve))
	}
	syntax.FrameClose(&b)
	return b.String()
}
