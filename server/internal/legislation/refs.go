// RUYI-869 (owner instruction 2026-10-24): the structured-clause reference
// contract, enforced by the legislation gate fail-closed. Clauses carry a
// short ASCII identity (cls_ + 8 lowercase alphanumerics, clausemark.NewSlug)
// and reference each other — and are referenced by the static carrier
// context — by that slug. Three violation classes are gate errors:
//
//  1. 缺失引用 — a ref whose slug is not in the post-enact clause universe:
//     it points at nothing the runtime will inject.
//  2. 循环引用 — a reference cycle between clause bodies: cyclic clauses
//     re-introduce the circular mention loops the owner banned; refs must
//     form a DAG.
//  3. 反向静态引用 — a clause body citing the static section heading of the
//     carrier it is bound to: the signal the clause was not extracted
//     cleanly; the rule belongs in the static text instead.
//
// Plus the deterministic-ordering invariant: when a post-enact carrier text
// carries marker regions, their open tags must sit in ascending slug order
// — the same total order the effective-set SQL (ORDER BY scope_rank,
// c.slug) and clausemark.Render emit; re-rendering is byte-stable only
// under it. The gate judges the legacy engine's synthesis INTERMEDIATE,
// which may carry marker fragments the re-renderer (clausemark.Render's
// tolerant StripMarkers) converges before the text lands in the carrier
// column — so the region inventory is observed with the same tolerant
// open-tag scan (clausemark.ScanMarkers), never a strict Parse: a region
// the re-renderer heals must not gate-block the proposal that produced it.
package legislation

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/pkg/clausemark"
)

// refRe matches a clause slug embedded in running text: cls_ + exactly 8
// lowercase alphanumerics, not glued into a longer alnum run. The word
// boundaries carry the glue check — a 9-character suffix or a prefixed
// alnum makes it not a reference.
var refRe = regexp.MustCompile(`\bcls_[0-9a-z]{8}\b`)

// RefUniverse is the reference universe one gate run validates against:
// the post-enact state of the workspace's active clause library, the
// post-enact text and static heading set of every carrier the enactment
// re-renders, and the current static context of every carrier it does not
// touch — the set a remove can orphan without the gate ever seeing it.
type RefUniverse struct {
	// Bodies: slug -> clause body in the post-enact state (the proposal's
	// own new/revised body replaces the stored one; a removed clause's
	// slug is absent).
	Bodies map[string]string
	// Binding: slug -> carrier key ("scope\x00uuid") the clause is in
	// force at.
	Binding map[string]string
	// Headings: carrier key -> the carrier's static `## ` section titles
	// (post-enact text).
	Headings map[string][]string
	// Texts: carrier key -> the post-enact carrier text of the carriers this
	// enactment re-renders; both the ordering check and the static
	// missing-ref check scan these.
	Texts map[string]string
	// StaticTexts: carrier key -> the CURRENT full text of a carrier the
	// enactment does NOT re-render. Its text is unchanged by this proposal,
	// so only the static missing-ref rule applies to it — a forward ref to a
	// clause that leaves the active set would otherwise dangle in the runtime
	// prompt. The ordering check never runs over it (its regions are
	// inherited debt the next successful enactment heals; scanning them here
	// would misreport that debt as newly introduced). Marker regions are
	// stripped tolerantly by the ref scan: their ids are snapshot debt, not
	// static references.
	StaticTexts map[string]string
	// CurrentTexts: carrier key -> the pre-proposal carrier text. Present so
	// rule 4 can tell a NEWLY introduced out-of-order region (error) from
	// inherited legacy disorder (warning — the re-renderer heals it on the
	// next successful enactment, so blocking would deadlock the carrier).
	CurrentTexts map[string]string
	// SelfSlug: the proposal's own clause slug. "" when the slug has not
	// been minted yet (add/migrate) — the proposed body is then checked
	// under the self alias.
	SelfSlug string
	// SelfBody: the proposal's own clause text after synthesis.
	SelfBody string
	// SelfCarrier: the carrier key the proposal's body lands at.
	SelfCarrier string
}

// SelfAlias is the graph node the not-yet-minted proposed body checks
// under; a ref to it is a self-cycle.
const SelfAlias = "_self_"

// CheckClauseRefs validates one gate run's reference universe. It returns
// gate findings in deterministic order (by class, then by slug); an empty
// slice means the proposal passes the whole reference contract.
func CheckClauseRefs(u *RefUniverse) []Finding {
	var out []Finding

	// Post-enact graph: every active clause body plus the proposal's own
	// body — under its slug once the universe already knows it, under the
	// self alias when not yet minted (add/migrate).
	bodyOf := map[string]string{}
	for s, b := range u.Bodies {
		bodyOf[s] = b
	}
	selfNode := u.SelfSlug
	if u.SelfBody != "" {
		if selfNode == "" {
			selfNode = SelfAlias
		}
		bodyOf[selfNode] = u.SelfBody // proposed body wins over stored
	}
	nodes := make([]string, 0, len(bodyOf))
	seen := map[string]bool{}
	for s := range bodyOf {
		nodes = append(nodes, s)
		seen[s] = true
	}
	sort.Strings(nodes)

	// refs: node -> deduped sorted target slugs; static carrier text is the
	// only other reference source, and the owner permits the forward
	// direction static -> clause, so it adds no constraint here.
	refs := map[string][]string{}
	for _, n := range nodes {
		targets := map[string]bool{}
		for _, line := range splitLines(bodyOf[n]) {
			for _, m := range refRe.FindAllString(line, -1) {
				targets[m] = true
			}
		}
		ts := make([]string, 0, len(targets))
		for t := range targets {
			ts = append(ts, t)
		}
		sort.Strings(ts)
		refs[n] = ts
	}

	// 1) 缺失引用: an edge whose target is not a node of the universe.
	for _, n := range nodes {
		for _, t := range refs[n] {
			if !seen[t] {
				out = append(out, Finding{Level: "error",
					Message: fmt.Sprintf("缺失引用：条款 %s 引用了不在空间 active 条款库中的编号 %s（引用必须指向 active 条款；不存在的编号运行时不会被注入）", n, t)})
			}
		}
	}

	// 2) 循环引用: the ref graph must be a DAG.
	if cyc := findCycle(nodes, refs); len(cyc) > 0 {
		out = append(out, Finding{Level: "error",
			Message: fmt.Sprintf("循环引用：条款引用环 %s（条款间引用必须是 DAG；环引用重新引入循环执行，禁止）", strings.Join(cyc, " → "))})
	}

	// 3) 反向静态引用: a clause body must not cite the static section
	// headings of the carrier it is bound to.
	carrierOf := func(n string) string {
		if n == SelfAlias {
			return u.SelfCarrier
		}
		return u.Binding[n]
	}
	for _, n := range nodes {
		headings, ok := u.Headings[carrierOf(n)]
		if !ok {
			continue
		}
		headings = append([]string(nil), headings...)
		sort.Strings(headings) // findings stay deterministic; copy so the
		// caller's slice is untouched
		lines := splitLines(bodyOf[n])
		for _, h := range headings {
			if h == "" {
				continue
			}
			for i, line := range lines {
				// CJK text has no word boundaries and RE2 has no lookarounds:
				// a clause body naming its carrier's section title is a
				// citation by any occurrence. Over-reporting is gate-side
				// only — the author reworks the text, so the check stays
				// inclusive.
				if strings.Contains(line, h) {
					out = append(out, Finding{Line: i + 1, Level: "error",
						Message: fmt.Sprintf("反向静态引用：条款 %s 正文引用了静态章节「%s」（条款不得反向指向载体静态章节；出现即条款未提炼干净，规则应落回静态提示词）", n, h)})
					break // one finding per (heading, clause)
				}
			}
		}
	}

	// 4) 确定性顺序: the well-formed open tags of a post-enact carrier
	// text must sit in ascending slug order. Region fragments the legacy
	// synthesis leaves behind (an orphaned closer, an unclosed opener)
	// are not judged here: the re-renderer's tolerant strip converges
	// them before the text lands in the carrier column. A DISORDER the
	// pre-proposal text already had is inherited legacy debt the re-renderer
	// heals on the next successful enactment — reporting it as an error
	// would deadlock that carrier behind itself — so it downgrades to a
	// warning. Disorder the proposal newly introduced is a hard error.
	//
	// 5) 静态缺失引用: the owner permits the forward direction — the static
	// carrier context naming active clause slugs — but a static text that
	// names a slug the post-enact universe no longer carries is the same
	// 缺失引用 class and fails closed. The scan covers BOTH the stable
	// halves of the post-enact texts of the carriers this enactment
	// re-renders (Texts) and the current static text of every carrier it
	// leaves untouched (StaticTexts): a remove must not orphan a forward
	// reference that lives in a sibling tier's prompt, which the re-render
	// never rewrites.
	findStaticMissingRefs := func(static, origin string) {
		for _, m := range refRe.FindAllString(static, -1) {
			if !seen[m] && m != SelfAlias {
				out = append(out, Finding{Level: "error",
					Message: fmt.Sprintf("缺失引用：%s 载体的静态上下文引用了不在空间 active 条款库中的编号 %s（引用必须指向 active 条款；不存在的编号运行时不会被注入）", origin, m)})
			}
		}
	}
	keys := make([]string, 0, len(u.Texts)+len(u.StaticTexts))
	for k := range u.Texts {
		keys = append(keys, k)
	}
	for k := range u.StaticTexts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		scope := strings.SplitN(k, "\x00", 2)[0]
		post, postOK := u.Texts[k]
		if !postOK {
			// Untouched carrier: the proposal leaves its text unchanged, so
			// only the static missing-ref rule can newly break. Marker
			// regions are that carrier's own snapshot debt (healed by the
			// next enactment that re-renders IT, out of this gate's scope),
			// so they are stripped tolerantly and their clause bodies are
			// not re-judged here — clause-body refs are rule 1's domain.
			findStaticMissingRefs(clausemark.StripMarkers(u.StaticTexts[k]), scope)
			continue
		}
		refs := clausemark.ScanMarkers(post)
		if len(refs) == 0 {
			// No markers: the whole text is static; a bare slug mention is
			// still a reference that must resolve.
			findStaticMissingRefs(post, scope)
			continue
		}
		// 5) The stable half — exactly what the re-renderer's tolerant
		// strip leaves before it re-emits the region — is the static
		// context; region bodies are rule 1's domain, not static refs.
		findStaticMissingRefs(clausemark.StripMarkers(post), scope)
		inherited := false
		if current, ok := u.CurrentTexts[k]; ok {
			inherited = hasOutOfOrderSegment(current)
		}
		for i := range refs {
			if i > 0 && refs[i].Slug < refs[i-1].Slug {
				level, what := "error", "条款区段顺序非升序（本提案引入）："
				if inherited {
					level, what = "warning", "条款区段顺序非升序（继承自提案前文本，下次成功立法渲染时自愈）："
				}
				out = append(out, Finding{Level: level,
					Message: what + fmt.Sprintf("区域 %s 出现在 %s 之后（确定性渲染要求按 slug 升序排列区段，渲染输出才会逐字节稳定）", refs[i].Slug, refs[i-1].Slug)})
			}
		}
	}

	return out
}

// hasOutOfOrderSegment reports whether text's marker open tags already sit
// out of ascending-slug order — the inherited-disorder signal for rule 4.
// It observes the well-formed open tags with clausemark.ScanMarkers, the
// same inventory the main scan uses: a fragment the re-renderer will heal
// degrades to "inherited", never blocks.
func hasOutOfOrderSegment(text string) bool {
	refs := clausemark.ScanMarkers(text)
	for i := range refs {
		if i > 0 && refs[i].Slug < refs[i-1].Slug {
			return true
		}
	}
	return false
}

// findCycle returns one reference cycle (start repeated last) when the
// directed graph has one, nil otherwise. Deterministic under the sorted
// node/edge order; the graph is small (one workspace's active clauses),
// so a simple DFS coloring pass suffices.
func findCycle(nodes []string, adj map[string][]string) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	stack := []string{}
	var cycle []string
	var dfs func(n string) bool
	dfs = func(n string) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range adj[n] {
			switch color[m] {
			case gray:
				start := 0
				for i, s := range stack {
					if s == m {
						start = i
						break
					}
				}
				cycle = append(append([]string{}, stack[start:]...), m)
				return true
			case white:
				if dfs(m) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}
	for _, n := range nodes {
		if color[n] == white && dfs(n) {
			return cycle
		}
	}
	return nil
}
