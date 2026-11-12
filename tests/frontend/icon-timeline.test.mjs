import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { clampView, clusterHits, effectBars, effectFace, effectGroupOf, fullView, isFullView, minimumViewMs, panView, segmentAt, viewAround, zoomView } from "../../frontend/src/lib/history-analysis.ts"

const source = path => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8")

test("zooming keeps the moment under the pointer and the window inside the encounter", () => {
  const duration = 120000
  assert.deepEqual(fullView(duration), { fromMs: 0, toMs: duration })
  assert.ok(isFullView(fullView(duration), duration))
  const zoomed = zoomView(fullView(duration), 0.5, 30000, duration)
  assert.equal(zoomed.toMs - zoomed.fromMs, 60000)
  // 30 s was a quarter of the way across; it still is.
  assert.ok(Math.abs((30000 - zoomed.fromMs) / (zoomed.toMs - zoomed.fromMs) - 0.25) < 1e-9)
  // Never narrower than a second, never wider than the encounter, never outside it.
  const tight = zoomView(zoomed, 1e-6, 50000, duration)
  assert.equal(tight.toMs - tight.fromMs, minimumViewMs)
  assert.deepEqual(zoomView(zoomed, 1000, 50000, duration), fullView(duration))
  assert.deepEqual(panView({ fromMs: 100000, toMs: 110000 }, 50000, duration), { fromMs: 110000, toMs: 120000 })
  assert.deepEqual(panView({ fromMs: 1000, toMs: 11000 }, -50000, duration), { fromMs: 0, toMs: 10000 })
  // An encounter shorter than a second is shown whole.
  assert.deepEqual(clampView({ fromMs: 100, toMs: 200 }, 600), { fromMs: 0, toMs: 600 })
  // A selected range is shown with some context, still inside the encounter.
  const around = viewAround({ fromMs: 10000, toMs: 20000 }, duration)
  assert.ok(around.fromMs < 10000 && around.toMs > 20000 && around.fromMs >= 0)
})

test("every hit in the window is drawn exactly once, grouped so icons never pile up", () => {
  // Three skill rows: a burst of nine hits within 200 ms, a steady hit every 2 s, and one hit inside the burst.
  const burst = Array.from({ length: 9 }, (_, i) => 10000 + i * 25)
  const steady = Array.from({ length: 30 }, (_, i) => i * 2000)
  const single = [10100]
  const series = [{ key: "a", atMs: burst }, { key: "b", atMs: steady }, { key: "c", atMs: single }]
  for (const [view, width] of [[fullView(60000), 1200], [{ fromMs: 9000, toMs: 12000 }, 1200], [fullView(60000), 240], [{ fromMs: 9990, toMs: 10210 }, 1500]]) {
    const clusters = clusterHits(series, view, width, 24, 2)
    const inView = series.flatMap(one => one.atMs).filter(at => at >= view.fromMs && at <= view.toMs).length
    const drawn = clusters.reduce((count, cluster) => count + cluster.count, 0)
    assert.equal(drawn, inView, `${JSON.stringify(view)} at ${width}px`)
    // Each hit index of each series appears once.
    const seen = new Set()
    for (const cluster of clusters) for (const member of cluster.members) for (const hit of member.hits) {
      const id = `${member.series}:${hit}`
      assert.ok(!seen.has(id), `hit ${id} drawn twice`)
      seen.add(id)
      const at = series[member.series].atMs[hit]
      assert.ok(at >= cluster.fromMs && at <= cluster.toMs, "a group spans its hits")
    }
    // Icons in one lane never overlap: each starts at least one icon width after the one before it.
    for (const lane of [0, 1]) {
      const starts = clusters.filter(cluster => cluster.lane === lane).map(cluster => cluster.x * width).sort((x, y) => x - y)
      for (let i = 1; i < starts.length; i++) assert.ok(starts[i] - starts[i - 1] >= 24 - 1e-6, `lane ${lane}: ${starts[i - 1]} then ${starts[i]}`)
    }
    assert.ok(clusters.every(cluster => cluster.lane === 0 || cluster.lane === 1))
  }
  // Zoomed far enough in, the burst is nine separate icons; zoomed out it is fewer icons holding the same hits.
  const near = clusterHits([series[0]], { fromMs: 9990, toMs: 10210 }, 1500, 24, 2)
  assert.equal(near.length, 9)
  const far = clusterHits([series[0]], fullView(60000), 1200, 24, 2)
  assert.equal(far.length, 1)
  assert.equal(far[0].count, 9)
  assert.deepEqual(clusterHits(series, fullView(60000), 0, 24), [])
})

test("touching intervals of one evidence draw as one bar; evidence never joins and every interval stays reachable", () => {
  const segments = [
    { fromMs: 0, toMs: 1000, evidence: "confirmed", stack: 1 },
    { fromMs: 1000, toMs: 2000, evidence: "confirmed", stack: 3 },
    { fromMs: 2000, toMs: 3000, evidence: "confirmed", stack: 3 },
    { fromMs: 3000, toMs: 4000, evidence: "stated", stack: 3 },
    { fromMs: 4000, toMs: 5000, evidence: "unknown", stack: 3 },
    { fromMs: 6000, toMs: 7000, evidence: "confirmed" },
  ]
  const bars = effectBars(segments)
  assert.deepEqual(bars.map(bar => [bar.fromMs, bar.toMs, bar.evidence, bar.segments]), [
    [0, 3000, "confirmed", [0, 1, 2]], [3000, 4000, "stated", [3]], [4000, 5000, "unknown", [4]], [6000, 7000, "confirmed", [5]],
  ])
  // A stack count above 1 is labelled once per run.
  assert.deepEqual(bars[0].stacks, [{ fromMs: 1000, toMs: 3000, stack: 3 }])
  assert.deepEqual(bars[1].stacks, [{ fromMs: 3000, toMs: 4000, stack: 3 }])
  assert.deepEqual(bars.flatMap(bar => bar.segments).sort((a, b) => a - b), [0, 1, 2, 3, 4, 5])
  // A pick inside a bar finds the exact interval under it, or the nearest one.
  assert.equal(segmentAt(segments, bars[0].segments, 1500), 1)
  assert.equal(segmentAt(segments, bars[0].segments, 2999), 2)
  assert.equal(segmentAt(segments, bars[0].segments, 3500), 2)
  assert.equal(segmentAt(segments, [], 10), -1)
})

test("an effect shows its own image only when Go found one, its term with the neutral mark, or the unknown mark with its ID", () => {
  assert.equal(effectFace({ icon: true, name: { status: "UNKNOWN" } }), "icon")
  assert.equal(effectFace({ icon: true, name: { name: "Term", status: "CORRELATED" } }), "icon")
  assert.equal(effectFace({ icon: false, name: { name: "Term", status: "CORRELATED" } }), "named")
  assert.equal(effectFace({ icon: false, name: { status: "UNKNOWN" } }), "unknown")
})

test("effects are grouped by the holder Go resolved; an unknown holder is never given to a role", () => {
  assert.deepEqual(effectGroupOf({ kind: "team" }), { kind: "team", key: "team" })
  assert.deepEqual(effectGroupOf({ kind: "team_unassigned" }), { kind: "team", key: "team" })
  assert.deepEqual(effectGroupOf({ kind: "role", roleId: 1505, contributor: "role-7-1505" }), { kind: "role", key: "role:role-7-1505" })
  // A summon's effects sit with its owner's.
  assert.deepEqual(effectGroupOf({ kind: "summon", roleId: 1505, contributor: "role-7-1505" }), { kind: "role", key: "role:role-7-1505" })
  assert.deepEqual(effectGroupOf({ kind: "role", roleId: 1206 }), { kind: "role", key: "role:id-1206" })
  assert.deepEqual(effectGroupOf({ kind: "enemy" }), { kind: "enemy", key: "enemy" })
  for (const kind of ["other", "unknown", "vehicle"]) assert.deepEqual(effectGroupOf({ kind }), { kind: "unknown", key: "unknown" })
})

test("the icon timeline draws Go's answers: icons by Go's flag, skill images as the Skills table draws them, no ID resolution in React", () => {
  const view = source("frontend/src/views/HistoryAnalysis.tsx")
  const timeline = source("frontend/src/design/components/IconTimeline.tsx")
  // Effect images only for a row Go marked, by its own Buff.Id; the precise lanes follow the same flag.
  assert.match(view, /icon: row\.icon && row\.effectId !== undefined \? \{ kind: "buff", id: row\.effectId \} : null/)
  assert.match(view, /const icon = row\.icon && row\.effectId !== undefined/)
  assert.doesNotMatch(view, /row\.hudIcon && row\.effectId/)
  // A skill row uses the image and name of its Skills table row (the encounter's own projection).
  assert.match(view, /icon: \{ kind: row\.iconKind \?\? "skill", id: row\.iconId \?\? row\.skillId \}/)
  assert.match(source("frontend/src/views/DashboardView.tsx"), /<GameAssetImage kind=\{row\.iconKind \?\? "skill"\} id=\{row\.iconId \?\? row\.skillId\} \/>/)
  // No statistic, coverage or attribution is computed for the timeline; a group's count is the number of icons it holds.
  for (const text of [view, timeline]) {
    assert.doesNotMatch(text, /amount\.reduce|confirmedMs\s*\/|\.reduce\(\(sum/)
  }
  // Hits are labelled as hits, never casts, and the details say a group is no cast.
  for (const locale of ["zh-TW", "zh-CN", "en", "ja", "ko"]) {
    const history = JSON.parse(source(`frontend/locales/${locale}/history.json`))
    assert.ok(history.analysis.icon.skillsNote && history.analysis.icon.hits.note, locale)
  }
})

test("the two presentations share one window, one drag and one selection, and switching keeps the loaded data", () => {
  const view = source("frontend/src/views/HistoryAnalysis.tsx")
  // Both presentations sit under the same DPS lines with the same window and the same drag preview.
  assert.match(view, /<AnalysisTimeline [^>]*view=\{view\} sync=\{sync\}/)
  assert.match(view, /<IconTimeline [^>]*view=\{view\}[^>]*range=\{selected\} sync=\{sync\}/)
  assert.match(view, /<TimelineLanes [^>]*view=\{view\} sync=\{sync\}/)
  // The History page keeps the mode from one opened encounter to the next; hits are read once per filter, not per switch.
  assert.match(source("frontend/src/views/HistoryView.tsx"), /const \[timelineMode, setTimelineMode\] = useState<TimelineMode>\("icon"\)/)
  assert.match(view, /if \(mode !== "icon" \|\| hitsReady\) return/)
  // The hits are read beside the analysis and the lanes, not after the card mounts.
  const fetchAt = view.indexOf("host.historyHits(")
  assert.ok(fetchAt > view.indexOf("export function AnalysisWorkspace") && fetchAt < view.indexOf("const TimelineCard = memo("))
  // An effect picked in the icon timeline is the shared focus of the lanes and the Effects tab.
  assert.match(view, /if \(next\?\.kind === "effect"\) setFocus\(\{ kind: "effect", key: next\.track \}\)/)
  // Zooming is presentation: it never changes the query.
  assert.doesNotMatch(view, /setQuery\([^)]*view/)
})

test("the drag and the pointer moment reach the other tracks without re-rendering them", () => {
  const timeline = source("frontend/src/design/components/AnalysisTimeline.tsx")
  assert.match(timeline, /useLayoutEffect\(\(\) => \{ sync\?\.set\(\{ preview: drag \}\) \}, \[sync, drag\]\)/)
  assert.match(timeline, /const preview = useTimelineSync\(sync, "preview"\)/)
  const icon = source("frontend/src/design/components/IconTimeline.tsx")
  // Only the overlay subscribes; the rows are memoised on their own data and the window.
  assert.match(icon, /<TrackOverlay className="ww-itl-overlay" view=\{view\} range=\{range\} gaps=\{gaps\} sync=\{sync\} \/>/)
  assert.match(icon, /const HitRow = memo\(/)
  assert.match(icon, /const EffectRow = memo\(/)
  assert.doesNotMatch(icon, /useTimelineSync/)
  // Only the rows in view are mounted, and only what lies in the window is drawn.
  assert.match(icon, /const shownRows = rows\.slice\(first, last\)/)
  assert.match(icon, /if \(!overlaps\(bar\.fromMs, bar\.toMs, view\)\) return null/)
})
