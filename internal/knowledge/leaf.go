// Package knowledge is plumb's tree of what a kernel signal means and what can
// be done about it.
//
// It exists because the diagnostic output has to stay short, and a short output
// cannot also explain itself. The split is deliberate: the report says what is
// wrong on this machine right now, and this tree is what you read when you want
// the rest — what the thing actually is, why it shows up, what to do, and where
// to check for yourself.
//
// The keys are what make this different from searching the web. A search takes a
// fuzzy symptom ("the server is slow") and guesses. plumb arrives holding
// jbd2_log_wait_commit, which names exactly one place in the kernel: no
// ambiguity, nothing to rank, and no room for a page written to match a search
// engine rather than the problem. So the tree is keyed by the things plumb
// actually observes — a wchan symbol, one of its own checks, a /proc field — and
// every leaf is a fixed, offline, checkable answer.
//
// Three rules shape every leaf:
//
//  1. It carries its sources. A leaf that cannot cite where it came from is a
//     leaf nobody can check, and an unchecked explanation is exactly what this
//     project exists to avoid. Sources are given as paths into the kernel
//     source tree or its documentation, so they stay valid across documentation
//     reorganisations — a URL rots, fs/jbd2/commit.c does not.
//
//  2. It states how far plumb will stand behind it. Some of these things have
//     one standard fix. Some depend on facts that are not in /proc at all — a
//     host stealing CPU from a guest, a storage array on the far end of a
//     fabric. A few plumb can only name. Saying which is which is more useful
//     than a confident sentence, and it is the only honest option: a wrong
//     explanation gets acted on, and the person acting on it has no way to tell.
//
//  3. When there is nothing to offer, it says so instead of padding. A leaf with
//     an empty Fix and a filled Unknown is a leaf doing its job — "here is what
//     this symptom means, and plumb cannot help you past that" is a real answer,
//     and it is a better one than a guess dressed up as advice.
//
// The package holds no I/O and no formatting: it is data and lookup, so it can
// be exercised without capturing any output. Rendering lives with the other
// output code.
package knowledge

import "strings"

// Kind is the family a key belongs to.
type Kind int

const (
	// KindWChan is a kernel function a thread can be parked in — the value
	// /proc/<pid>/wchan reports. These are the sharpest keys in the tree.
	KindWChan Kind = iota
	// KindArea is one of the checks plumb runs, by the name the report prints.
	KindArea
	// KindProc is a file or field a number was read from.
	KindProc
	// KindStack is a kernel subsystem a thread's call chain resolves to —
	// read from /proc/<pid>/task/<tid>/stack, not from wchan. It is kept a
	// separate kind because it answers a different question: wchan says
	// which function, a stack says which subsystem decided to wait.
	KindStack
)

func (k Kind) String() string {
	switch k {
	case KindWChan:
		return "内核等待点"
	case KindArea:
		return "判定项"
	case KindProc:
		return "数据来源"
	case KindStack:
		return "等待子系统"
	}
	return "未知类别"
}

// Confidence is how far plumb is willing to stand behind a leaf.
type Confidence int

const (
	// Certain means the cause and the fix both have kernel source or
	// documentation behind them, and the fix is the standard one.
	Certain Confidence = iota
	// Rough means the cause is clear but the fix depends on something plumb
	// cannot read from /proc. What the leaf holds is the direction, not the
	// answer.
	Rough
	// NameOnly means plumb can say what the symptom means and nothing more.
	// Some of these are not fixable from inside the machine at all, and the
	// leaf says that rather than inventing a step.
	NameOnly
)

func (c Confidence) String() string {
	switch c {
	case Certain:
		return "有依据"
	case Rough:
		return "只说方向"
	case NameOnly:
		return "只知症状"
	}
	return "未知"
}

// Note is the sentence printed at the end of a leaf that is not Certain. It
// exists so that a reader never has to guess which parts are solid: the leaf
// says it itself.
func (c Confidence) Note() string {
	switch c {
	case Certain:
		return ""
	case Rough:
		return "这一片给的是方向，不是处方 —— 具体怎么定，取决于 plumb 在这台机器上看不到的东西。"
	case NameOnly:
		return "这一片到此为止：plumb 能说明这个症状意味着什么，但给不出处置，也不打算编一个。"
	}
	return ""
}

// Leaf is one node of the tree.
type Leaf struct {
	// Key is what -why takes. For a wchan leaf it is the kernel function the
	// report would have shown; for an area it is the exact string the report
	// prints; for a proc leaf it is the file or field path.
	Key  string
	Kind Kind
	// Alias holds the other names the same leaf answers to: sibling functions
	// in one family, the old name of a renamed symbol, the short label a person
	// would actually type.
	Alias []string
	// What is the mechanism, not a restatement of the symptom. Its first
	// sentence doubles as the catalogue line.
	What string
	// Why lists the situations that produce this signal.
	Why []string
	// Fix lists what to do, most effective first. Empty is allowed and means
	// plumb has nothing honest to add — see Confidence.
	Fix []string
	// Unknown records the edge of what this leaf can tell you. Empty means
	// there is no such edge worth mentioning.
	Unknown string
	// Sources are where the claims above can be checked. At least one is
	// required; a leaf without one does not belong in the tree.
	Sources []string
	// Confidence says how much of the above plumb stands behind.
	Confidence Confidence
}

// Line is the catalogue entry for this leaf: the first sentence of What.
//
// Every leaf is written so that sentence stands alone as a one-line summary,
// which is why there is no separate summary field to drift out of step with the
// body. A test holds the whole tree to that shape.
func (l Leaf) Line() string {
	s := strings.TrimSpace(l.What)
	cut := len(s)
	for _, sep := range []string{"。", "；", "！", "？", "\n"} {
		if i := strings.Index(s, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(s[:cut])
}

// tree is every leaf, grouped by kind so the catalogue reads in a sensible
// order. Lookup does not care about the order.
var tree = buildTree()

func buildTree() []Leaf {
	var all []Leaf
	all = append(all, wchanLeaves...)
	all = append(all, stackLeaves...)
	all = append(all, areaLeaves...)
	all = append(all, procLeaves...)
	return all
}

// All returns every leaf in catalogue order.
func All() []Leaf { return tree }

// Kinds lists every kind, in the order the catalogue prints them.
//
// The catalogue ranges over this rather than over a literal list of three,
// because a kind that is added to the tree and forgotten here does not fail —
// it just vanishes from the listing, and the tree quietly shrinks for anyone
// browsing it. TestKindsCoverTheTree is what makes that a build error rather
// than a missing section.
func Kinds() []Kind {
	return []Kind{KindWChan, KindStack, KindArea, KindProc}
}

// Find resolves a key to the leaves that match it.
//
// Matching runs from strictest to loosest and stops at the first level that
// matches anything: an exact key, an exact alias, a key prefix, a substring,
// and finally the family prefix of a kernel name (nvme_poll is not a leaf, but
// nvme_ is, and for a family like that the family is the right answer).
//
// Stopping at the first level is what keeps the result meaningful. It also means
// ambiguity is returned rather than resolved: when a needle matches several
// leaves, the caller gets all of them, because quietly picking one would be
// plumb answering a question nobody asked.
func Find(needle string) []Leaf {
	n := strings.ToLower(strings.TrimSpace(needle))
	if n == "" {
		return nil
	}
	fam := family(n)

	var exact, alias, prefix, sub, familyMatch []Leaf
	for _, l := range tree {
		key := strings.ToLower(l.Key)
		switch {
		case key == n:
			exact = append(exact, l)
		case hasExact(l.Alias, n):
			alias = append(alias, l)
		case strings.HasPrefix(key, n):
			prefix = append(prefix, l)
		case strings.Contains(key, n) || hasSubstring(l.Alias, n):
			sub = append(sub, l)
		case fam != "" && hasExact(l.Alias, fam):
			familyMatch = append(familyMatch, l)
		}
	}
	for _, set := range [][]Leaf{exact, alias, prefix, sub, familyMatch} {
		if len(set) > 0 {
			return set
		}
	}
	return nil
}

// Nearby returns keys that look like the needle, for the message printed when
// nothing matched. It is limited on purpose: a long list would be a catalogue,
// and the catalogue already has a way to be asked for.
func Nearby(needle string, limit int) []string {
	n := strings.ToLower(strings.TrimSpace(needle))
	if n == "" || limit <= 0 {
		return nil
	}
	first := n
	if i := strings.IndexAny(first, "_-"); i > 0 {
		first = first[:i]
	}
	var out []string
	for _, l := range tree {
		if len(out) >= limit {
			break
		}
		key := strings.ToLower(l.Key)
		if strings.Contains(key, first) || strings.Contains(n, key) {
			out = append(out, l.Key)
		}
	}
	return out
}

// hasExact reports whether any candidate equals s.
func hasExact(candidates []string, s string) bool {
	for _, c := range candidates {
		if strings.ToLower(c) == s {
			return true
		}
	}
	return false
}

// hasSubstring reports whether any candidate contains s.
func hasSubstring(candidates []string, s string) bool {
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c), s) {
			return true
		}
	}
	return false
}

// family returns the leading token of a kernel function name, including the
// underscore: nvme_poll -> "nvme_", rwsem_down_write_slowpath -> "rwsem_".
//
// It is a guess, and the last resort in Find, because kernel names share a
// family prefix far more often than they collide: a thread parked in
// nvme_process_cq is in the NVMe driver, and saying so is better than saying
// nothing. Only the first token is used, so the guess cannot wander far from
// what was asked.
func family(n string) string {
	if i := strings.Index(n, "_"); i > 0 {
		return n[:i+1]
	}
	return ""
}
