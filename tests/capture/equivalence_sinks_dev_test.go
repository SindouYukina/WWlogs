//go:build wwdev

package capture

// equivalenceSinksSupported reports that this profile has diagnostic sinks the collector can attach.
const equivalenceSinksSupported = true

// attachEquivalenceSinks connects hashing writers to the diagnostic sinks that carry event, lifecycle,
// and snapshot records. Attaching them must not change the core state, which the collector verifies.
func attachEquivalenceSinks(p *Processor) *equivalenceSinkSet {
	set := &equivalenceSinkSet{sinks: map[string]*equivalenceHashSink{
		"combatEventsJSONL": newEquivalenceHashSink(),
		"buffLifecycle":     newEquivalenceHashSink(),
		"buffSnapshots":     newEquivalenceHashSink(),
	}}
	p.Dev.combatEventsJSONL = set.sinks["combatEventsJSONL"]
	p.Dev.buffLifecycle = set.sinks["buffLifecycle"]
	p.Dev.buffSnapshots = set.sinks["buffSnapshots"]
	return set
}
