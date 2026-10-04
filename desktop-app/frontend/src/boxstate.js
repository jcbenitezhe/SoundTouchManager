// Pure readers of a discovered speaker record's STM state, shared by the views
// and unit-tested without a DOM. The record shape is BoxInfo from the Go side
// (desktop-app/app_discovery.go).

// answersWithoutSTM reports whether the speaker's Bose firmware answered while
// the STM agent on it did not: a record discovery already degraded to "STM not
// running", a plain stock record, or this refresh's transient stmSilent marker
// (stock :8090 answered, agent port silent, box still counted as STM). False
// for a record that is missing or offline, which is the genuine "nothing
// answers, unplug it" case. Speaker Settings uses this to pick the honest
// ending for its reconnect loop (2026-09-06 report: a stock speaker that was
// answering fine got the "agent died, unplug the speaker" panel).
export function answersWithoutSTM(rec) {
  if (!rec || rec.offline) return false;
  return !!(rec.stmSilent || rec.stmNotRunning || rec.kind === 'stock');
}
