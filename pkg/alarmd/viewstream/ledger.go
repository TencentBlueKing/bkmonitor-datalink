// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"sort"
	"sync"
	"time"
)

// Receiver is one Worker process: the Worker's id and the incarnation of
// the process, so a restart is a different receiver and a receipt from the
// old process cannot complete a version for the new one.
type Receiver struct {
	WorkerID    string
	Incarnation string
}

// Receipt is what a Worker reports about one version. The stage facts are
// explicit and cumulative in meaning -- a Worker that installed has acked --
// but the ledger does not infer one from another: a receipt that asserts
// switched without installed does not count as installed, and the Worker
// resends the complete facts. Failure is why a stage was not reached, in
// the Worker's bounded words; ObjectsMissing is how many of the whole
// installed view's objects the Worker could not read when it installed,
// which is a fact of the installed view and not a failure of installation.
// It means nothing unless ObjectsProbed: a Worker whose probe failed knows
// nothing about its objects and does not say 0.
type Receipt struct {
	Receiver       Receiver
	Version        Version
	Acked          bool
	Installed      bool
	Switched       bool
	Failure        string
	ObjectsMissing int
	ObjectsProbed  bool
	// SwitchedQueryGroups is how many of the version's Query Groups the
	// Worker executes from the view; Switched is this reaching the version's
	// entry count. Carried so the Leader can say how many are not, per
	// Worker, rather than only that some are.
	SwitchedQueryGroups int
}

// Counts are the four numbers of one version, with the receivers the
// publication expected. Always 0 <= Switched <= Installed <= Acked <= Sent
// <= Expected; each receiver is counted once per stage however many times
// it reports.
type Counts struct {
	Expected, Sent, Acked, Installed, Switched int
}

// Complete reports a version every expected receiver switched to. Missing
// any one stage for any one receiver, it is not complete, and nothing
// completes it but that receiver's facts.
func (counts Counts) Complete() bool {
	return counts.Expected > 0 && counts.Switched == counts.Expected
}

// Outcome is what became of a version the ledger no longer follows.
type Outcome struct {
	Version  Key
	Counts   Counts
	OpenedAt time.Time
	ClosedAt time.Time
	// Reason is why it closed short of complete: "superseded" when a newer
	// version was published first, "" when it completed.
	Reason string
}

// Ignored counts receipts the ledger could not attribute, by why. They are
// facts about the fleet -- a receipt from a receiver no version expected is
// a Worker the Leader does not know, a receipt for a digest the version does
// not have is a Worker that installed something else -- and each is a count
// rather than an error, because nothing about the Leader's own state is
// wrong when they happen.
type Ignored struct {
	UnknownVersion     int
	UnexpectedReceiver int
	DigestMismatch     int
	StaleIncarnation   int
}

type receiverState struct {
	// digest is the digest of this receiver's own view at the version; a
	// receipt naming another digest is a Worker that installed something
	// else and is not counted.
	digest                           string
	incarnation                      string
	sent, acked, installed, switched bool
	failure                          string
	objectsMissing                   int
	objectsProbed                    bool
	// switchedQueryGroups is the Worker's latest count of Query Groups it
	// executes from the view, whether or not that reached switched.
	switchedQueryGroups int
}

type versionLedger struct {
	version   Key
	openedAt  time.Time
	receivers map[string]*receiverState
	// installedByAllAt is when the last expected receiver reported the
	// version installed; zero until then. The interval from openedAt is
	// how long the fleet took to hold one publication, which is the number
	// decision-016 section 10 step 4 measures.
	installedByAllAt time.Time
}

func (ledger *versionLedger) counts() Counts {
	counts := Counts{Expected: len(ledger.receivers)}
	for _, state := range ledger.receivers {
		if state.sent {
			counts.Sent++
		}
		if state.sent && state.acked {
			counts.Acked++
		}
		if state.sent && state.acked && state.installed {
			counts.Installed++
		}
		if state.sent && state.acked && state.installed && state.switched {
			counts.Switched++
		}
	}
	return counts
}

// Ledger follows the four numbers of the current version and the one before
// it. Older versions are closed with an Outcome the caller logs; the ledger
// keeps no history beyond the two, so it cannot grow with the number of
// publications.
type Ledger struct {
	mu       sync.Mutex
	now      func() time.Time
	current  *versionLedger
	previous *versionLedger
	ignored  Ignored
	closed   []Outcome
}

func NewLedger(now func() time.Time) *Ledger {
	if now == nil {
		now = time.Now
	}
	return &Ledger{now: now}
}

// Open starts following a version with the receivers the publication
// expects and the digest each one's view has, frozen here: a Worker that
// appears later is not added to this version's denominator and a Worker
// that leaves is not removed from it. The version before the previous one,
// if still open, is closed as superseded and its Outcome returned for the
// caller to log.
func (ledger *Ledger) Open(version Key, digests map[string]string) []Outcome {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	receivers := make(map[string]*receiverState, len(digests))
	for worker, digest := range digests {
		if worker != "" {
			receivers[worker] = &receiverState{digest: digest}
		}
	}
	var outcomes []Outcome
	if ledger.previous != nil {
		outcomes = append(outcomes, ledger.close(ledger.previous, "superseded"))
	}
	ledger.previous, ledger.current = ledger.current, &versionLedger{version: version, openedAt: ledger.now(), receivers: receivers}
	return outcomes
}

func (ledger *Ledger) close(entry *versionLedger, reason string) Outcome {
	counts := entry.counts()
	if counts.Complete() {
		reason = ""
	}
	return Outcome{Version: entry.version, Counts: counts, OpenedAt: entry.openedAt, ClosedAt: ledger.now(), Reason: reason}
}

func (ledger *Ledger) find(version Key) *versionLedger {
	if ledger.current != nil && ledger.current.version == version {
		return ledger.current
	}
	if ledger.previous != nil && ledger.previous.version == version {
		return ledger.previous
	}
	return nil
}

// MarkSent records that the complete message of a version was handed to
// the transport for a receiver. Handing it over is not the receiver having
// it; that is the acked stage, which only a receipt moves.
func (ledger *Ledger) MarkSent(version Key, receiver Receiver) bool {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry := ledger.find(version)
	if entry == nil {
		ledger.ignored.UnknownVersion++
		return false
	}
	state, expected := entry.receivers[receiver.WorkerID]
	if !expected {
		ledger.ignored.UnexpectedReceiver++
		return false
	}
	if state.incarnation != "" && state.incarnation != receiver.Incarnation {
		// The Worker restarted under this version: the old process's stages
		// are void, the new one starts from sent.
		*state = receiverState{digest: state.digest}
	}
	state.incarnation = receiver.Incarnation
	state.sent = true
	return true
}

// Recorded is what one receipt did to the ledger: whether it was
// attributed, and, when it was the receipt that completed the installed
// stage for every expected receiver of its version, how long that took
// from the version's publication. InstalledByAll is false for every other
// receipt, including repeats after the completing one.
type Recorded struct {
	Attributed     bool
	InstalledByAll bool
	Version        Key
	Expected       int
	Elapsed        time.Duration
}

// Record attributes a receipt to its version and receiver, or counts why it
// could not. Stages are recorded as asserted and counted in order, so a
// receipt asserting a later stage without an earlier one moves nothing the
// earlier gates; a Worker's later complete receipt does.
//
// An installed receipt speaks for the receiver's objects, one way or the
// other: every one the Worker sends carries what its probe found -- the
// count, or that it could not probe.
func (ledger *Ledger) Record(receipt Receipt) Recorded {
	return ledger.record(receipt, true)
}

// RecordClaimed attributes what a Worker claims at its Hello: that it has
// installed the version, so acked and installed. A Hello cannot say what the
// Worker's probe found, and a claim that says nothing about the objects leaves
// what is in hand where it is -- the Worker's next probe speaks. It is a
// separate entry point rather than a receipt with a flag unset, because on
// the wire an unset flag is a probe that failed, and the two must not read
// the same.
func (ledger *Ledger) RecordClaimed(receiver Receiver, version Version) Recorded {
	return ledger.record(Receipt{Receiver: receiver, Version: version, Acked: true, Installed: true}, false)
}

func (ledger *Ledger) record(receipt Receipt, objectsReported bool) Recorded {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry := ledger.find(receipt.Version.Key())
	if entry == nil {
		ledger.ignored.UnknownVersion++
		return Recorded{}
	}
	state, expected := entry.receivers[receipt.Receiver.WorkerID]
	if !expected {
		ledger.ignored.UnexpectedReceiver++
		return Recorded{}
	}
	if receipt.Version.Digest != state.digest {
		ledger.ignored.DigestMismatch++
		return Recorded{}
	}
	if state.incarnation != "" && state.incarnation != receipt.Receiver.Incarnation {
		ledger.ignored.StaleIncarnation++
		return Recorded{}
	}
	state.incarnation = receipt.Receiver.Incarnation
	state.acked = state.acked || receipt.Acked
	state.installed = state.installed || receipt.Installed
	state.switched = state.switched || receipt.Switched
	if receipt.Installed {
		state.switchedQueryGroups = receipt.SwitchedQueryGroups
	}
	if receipt.Failure != "" {
		state.failure = receipt.Failure
	}
	// An installed receipt is the receiver's current word on its objects:
	// the count it probed, or that it could not probe. A Worker whose probe
	// failed has no count, and the count it had from a probe that succeeded
	// earlier is not one now -- read on, it made "cannot tell" look like
	// "nothing missing" for as long as the probe kept failing. Only a claim
	// at a Hello, which cannot speak for the objects, leaves what is in
	// hand where it is.
	if receipt.Installed && objectsReported {
		state.objectsProbed, state.objectsMissing = receipt.ObjectsProbed, receipt.ObjectsMissing
	}
	recorded := Recorded{Attributed: true, Version: entry.version, Expected: len(entry.receivers)}
	if entry.installedByAllAt.IsZero() {
		if counts := entry.counts(); counts.Expected > 0 && counts.Installed == counts.Expected {
			entry.installedByAllAt = ledger.now()
			recorded.InstalledByAll, recorded.Elapsed = true, entry.installedByAllAt.Sub(entry.openedAt)
		}
	}
	return recorded
}

// countDigestMismatch counts a claim of a version of this term that names
// a view the publisher never gave the claimant: the same fact as a receipt
// with another digest, met at the Hello.
func (ledger *Ledger) countDigestMismatch() {
	ledger.mu.Lock()
	ledger.ignored.DigestMismatch++
	ledger.mu.Unlock()
}

// ObjectsSummary is what the installed receivers of a version said about
// their objects: how many receivers probed, how many could not, and the
// missing objects summed over those that probed. Unprobed receivers add
// nothing to the sum and are counted apart -- and named, since the page's
// question is which Worker cannot tell -- so a sum of 0 over a fleet that
// mostly could not probe is not read as a fleet with its objects.
type ObjectsSummary struct {
	Missing  int
	Probed   int
	Unprobed int
	// UnprobedWorkers are the installed receivers whose latest word was
	// that they could not probe, sorted.
	UnprobedWorkers []string
}

// Objects summarizes the object counts of a version's installed receivers.
func (ledger *Ledger) Objects(version Key) (ObjectsSummary, bool) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry := ledger.find(version)
	if entry == nil {
		return ObjectsSummary{}, false
	}
	summary := ObjectsSummary{}
	for worker, state := range entry.receivers {
		if !(state.sent && state.acked && state.installed) {
			continue
		}
		if state.objectsProbed {
			summary.Probed++
			summary.Missing += state.objectsMissing
		} else {
			summary.Unprobed++
			summary.UnprobedWorkers = append(summary.UnprobedWorkers, worker)
		}
	}
	sort.Strings(summary.UnprobedWorkers)
	return summary, true
}

// Counts of a version the ledger still follows; false for any other.
func (ledger *Ledger) Counts(version Key) (Counts, bool) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry := ledger.find(version)
	if entry == nil {
		return Counts{}, false
	}
	return entry.counts(), true
}

// Current is the version the ledger follows now and its counts.
func (ledger *Ledger) Current() (Key, Counts, bool) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.current == nil {
		return Key{}, Counts{}, false
	}
	return ledger.current.version, ledger.current.counts(), true
}

// Ignored is the running count of receipts the ledger could not attribute.
func (ledger *Ledger) Ignored() Ignored {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	return ledger.ignored
}

// Lagging lists the receivers of the current version that have not reached
// stage, sorted, with the failure each last reported. It is the answer to
// "who is holding this version", which the four numbers alone do not give.
func (ledger *Ledger) Lagging(stage string) []LaggingReceiver {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.current == nil {
		return nil
	}
	var lagging []LaggingReceiver
	for worker, state := range ledger.current.receivers {
		reached := map[string]bool{
			"sent": state.sent, "acked": state.sent && state.acked,
			"installed": state.sent && state.acked && state.installed,
			"switched":  state.sent && state.acked && state.installed && state.switched,
		}[stage]
		if reached {
			continue
		}
		lagging = append(lagging, LaggingReceiver{WorkerID: worker, Incarnation: state.incarnation, Failure: state.failure,
			SwitchedQueryGroups: state.switchedQueryGroups})
	}
	sort.Slice(lagging, func(left, right int) bool { return lagging[left].WorkerID < lagging[right].WorkerID })
	return lagging
}

// LaggingReceiver is one Worker short of a stage. Connected is filled by
// the server from its session table: a lagging Worker with no stream is a
// Worker that is gone or cannot reach the Leader, not one that is slow.
//
// It says nothing about the Worker's objects: a Worker short of installed
// has not probed anything. The pair of object fields that used to sit here
// was written for every lagging Worker and true for none, and read as a
// probe that had happened; what the installed receivers found is
// ObjectsSummary.
type LaggingReceiver struct {
	WorkerID    string
	Incarnation string
	Failure     string
	Connected   bool
	// SwitchedQueryGroups is the Worker's latest count of Query Groups it
	// executes from the view; read against the Worker's entry count on the
	// switched stage, it is how far short of switched the Worker is.
	SwitchedQueryGroups int
}
