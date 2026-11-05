package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/beadstest"
	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/dispatch"
	"github.com/gastownhall/gascity/internal/formulatest"
)

// seedDoneWorkflow builds a store holding a workflow root whose input convoy
// tracks a single member, plus one open step under the root. memberStatus
// controls whether the tracked work is terminal.
func seedDoneWorkflow(t *testing.T, memberStatus string) (*beads.MemStore, beads.Bead, beads.Bead) {
	t.Helper()
	store := beads.NewMemStore()
	work, err := store.Create(beads.Bead{Title: "the work"})
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if memberStatus == "closed" {
		if err := store.Close(work.ID); err != nil {
			t.Fatalf("close work: %v", err)
		}
	}
	if memberStatus == "handed_off" {
		if err := store.Update(work.ID, beads.UpdateOpts{Status: strPtr("open"), Assignee: strPtr("sysadmin/gastown.refinery")}); err != nil {
			t.Fatalf("update work: %v", err)
		}
	}
	conv, err := store.Create(beads.Bead{Title: "input convoy", Type: "convoy"})
	if err != nil {
		t.Fatalf("create convoy: %v", err)
	}
	if err := convoy.TrackItemIn(convoy.MemberClasses{Convoy: beadstest.Work(store)}, conv.ID, work.ID); err != nil {
		t.Fatalf("track: %v", err)
	}
	root, err := store.Create(beads.Bead{Title: "mol", Metadata: map[string]string{
		beadmeta.KindMetadataKey:          beadmeta.KindWorkflow,
		beadmeta.InputConvoyIDMetadataKey: conv.ID,
		beadmeta.RoutedToMetadataKey:      "rig/worker",
	}})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	step, err := store.Create(beads.Bead{Title: "Load context", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey:        root.ID,
		beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
		beadmeta.RoutedToMetadataKey:          "rig/worker",
	}})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	return store, root, step
}

func TestWorkflowInputDoneDetectsTerminalConvoyMember(t *testing.T) {
	store, root, step := seedDoneWorkflow(t, "closed")

	rootID, done, err := workflowInputDone(beadstest.Work(store), root, nil)
	if err != nil {
		t.Fatalf("workflowInputDone(root): %v", err)
	}
	if !done || rootID != root.ID {
		t.Fatalf("root: done=%v rootID=%q, want done=true rootID=%q", done, rootID, root.ID)
	}

	rootID, done, err = workflowInputDone(beadstest.Work(store), step, nil)
	if err != nil {
		t.Fatalf("workflowInputDone(step): %v", err)
	}
	if !done || rootID != root.ID {
		t.Fatalf("step: done=%v rootID=%q, want done=true rootID=%q", done, rootID, root.ID)
	}
}

func TestWorkflowInputDoneFalseWhileMemberOpen(t *testing.T) {
	store, root, _ := seedDoneWorkflow(t, "open")
	_, done, err := workflowInputDone(beadstest.Work(store), root, nil)
	if err != nil {
		t.Fatalf("workflowInputDone: %v", err)
	}
	if done {
		t.Fatal("done = true for an open convoy member, want false")
	}
}

func TestWorkflowInputDoneFalseWithoutInputConvoy(t *testing.T) {
	store := beads.NewMemStore()
	root, err := store.Create(beads.Bead{Metadata: map[string]string{
		beadmeta.KindMetadataKey: beadmeta.KindWorkflow,
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, done, err := workflowInputDone(beadstest.Work(store), root, nil)
	if err != nil {
		t.Fatalf("workflowInputDone: %v", err)
	}
	if done {
		t.Fatal("done = true with no input convoy, want false")
	}
}

func TestWorkflowInputDoneTrueWhenMemberHandedOffToOtherLane(t *testing.T) {
	store, root, _ := seedDoneWorkflow(t, "handed_off")
	_, done, err := workflowInputDone(beadstest.Work(store), root, []string{"gastown__polecat-gc-xxxx"})
	if err != nil {
		t.Fatalf("workflowInputDone: %v", err)
	}
	if !done {
		t.Fatal("done = false for a member handed off to another lane, want true")
	}
}

// A member still assigned to a seat that ran a step of this same workflow was
// not handed off: that seat is the workflow's own pool worker (typically dead,
// its routed steps already reopened). A successor seat must adopt the workflow,
// not retire it (sys-pxryan.44).
func TestWorkflowInputDoneFalseWhenMemberHeldBySiblingSeatOfSameWorkflow(t *testing.T) {
	store, root, step := seedDoneWorkflow(t, "open")
	const deadSeat = "homeops__homeops-luna-gc-5g2aze"
	claimStep, err := store.Create(beads.Bead{Title: "claim input", Assignee: deadSeat, Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey:        root.ID,
		beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
		beadmeta.RoutedToMetadataKey:          "rig/worker",
	}})
	if err != nil {
		t.Fatalf("create claim step: %v", err)
	}
	if err := store.Close(claimStep.ID); err != nil {
		t.Fatalf("close claim step: %v", err)
	}
	members, err := convoy.MembersIn(convoy.MemberClasses{Convoy: beadstest.Work(store)}, root.Metadata[beadmeta.InputConvoyIDMetadataKey], true)
	if err != nil || len(members) != 1 {
		t.Fatalf("convoy.Members = %d, %v; want 1 member", len(members), err)
	}
	if err := store.Update(members[0].ID, beads.UpdateOpts{Status: strPtr("in_progress"), Assignee: strPtr(deadSeat)}); err != nil {
		t.Fatalf("update work: %v", err)
	}

	_, done, err := workflowInputDone(beadstest.Work(store), step, []string{"homeops__homeops-luna-gc-9b2b0a"})
	if err != nil {
		t.Fatalf("workflowInputDone: %v", err)
	}
	if done {
		t.Fatal("done = true for a member held by a sibling seat of the same workflow, want false")
	}
}

// A seat that died before closing its step leaves that step reopened with the
// assignee cleared; only the step's gc.session_name/gc.session_id claim
// provenance still names it. That seat is still this workflow's own worker.
func TestWorkflowInputDoneFalseWhenMemberHeldBySiblingSeatWhoseStepWasReopened(t *testing.T) {
	store, root, step := seedDoneWorkflow(t, "open")
	const deadSeat = "homeops__homeops-luna-gc-5g2aze"
	if _, err := store.Create(beads.Bead{Title: "claim input", Status: "open", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey:        root.ID,
		beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
		beadmeta.RoutedToMetadataKey:          "rig/worker",
		beadmeta.SessionNameMetadataKey:       deadSeat,
		beadmeta.SessionIDMetadataKey:         "gc-5g2aze",
	}}); err != nil {
		t.Fatalf("create reopened claim step: %v", err)
	}
	members, err := convoy.MembersIn(convoy.MemberClasses{Convoy: beadstest.Work(store)}, root.Metadata[beadmeta.InputConvoyIDMetadataKey], true)
	if err != nil || len(members) != 1 {
		t.Fatalf("convoy.Members = %d, %v; want 1 member", len(members), err)
	}
	if err := store.Update(members[0].ID, beads.UpdateOpts{Status: strPtr("in_progress"), Assignee: strPtr(deadSeat)}); err != nil {
		t.Fatalf("update work: %v", err)
	}

	_, done, err := workflowInputDone(beadstest.Work(store), step, []string{"homeops__homeops-luna-gc-9b2b0a"})
	if err != nil {
		t.Fatalf("workflowInputDone: %v", err)
	}
	if done {
		t.Fatal("done = true for a member held by a sibling seat whose step was reopened, want false")
	}
}

// A reopened step may keep only gc.session_id as provenance; a member whose
// assignee is that session id is still held by this workflow's own seat.
func TestWorkflowInputDoneFalseWhenMemberHeldBySessionIDOnlyProvenance(t *testing.T) {
	store, root, step := seedDoneWorkflow(t, "open")
	const deadSessionID = "gc-5g2aze"
	if _, err := store.Create(beads.Bead{Title: "claim input", Status: "open", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey:        root.ID,
		beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
		beadmeta.RoutedToMetadataKey:          "rig/worker",
		beadmeta.SessionIDMetadataKey:         deadSessionID,
	}}); err != nil {
		t.Fatalf("create reopened claim step: %v", err)
	}
	members, err := convoy.MembersIn(convoy.MemberClasses{Convoy: beadstest.Work(store)}, root.Metadata[beadmeta.InputConvoyIDMetadataKey], true)
	if err != nil || len(members) != 1 {
		t.Fatalf("convoy.Members = %d, %v; want 1 member", len(members), err)
	}
	if err := store.Update(members[0].ID, beads.UpdateOpts{Status: strPtr("in_progress"), Assignee: strPtr(deadSessionID)}); err != nil {
		t.Fatalf("update work: %v", err)
	}

	_, done, err := workflowInputDone(beadstest.Work(store), step, []string{"homeops__homeops-luna-gc-9b2b0a"})
	if err != nil {
		t.Fatalf("workflowInputDone: %v", err)
	}
	if done {
		t.Fatal("done = true for a member held by a seat named only by gc.session_id provenance, want false")
	}
}

func TestWorkflowInputDoneFalseWhenMemberAssignedToIdentityCandidate(t *testing.T) {
	store, root, _ := seedDoneWorkflow(t, "handed_off")
	convoyID := root.Metadata[beadmeta.InputConvoyIDMetadataKey]
	members, err := convoy.MembersIn(convoy.MemberClasses{Convoy: beadstest.Work(store)}, convoyID, true)
	if err != nil {
		t.Fatalf("convoy.Members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members = %d, want 1", len(members))
	}
	if err := store.Update(members[0].ID, beads.UpdateOpts{Assignee: strPtr("gastown__polecat-gc-xxxx")}); err != nil {
		t.Fatalf("update work: %v", err)
	}
	_, done, err := workflowInputDone(beadstest.Work(store), root, []string{"gastown__polecat-gc-xxxx"})
	if err != nil {
		t.Fatalf("workflowInputDone: %v", err)
	}
	if done {
		t.Fatal("done = true for a member assigned to one of identityCandidates, want false")
	}
}

func TestWorkflowInputDoneFalseWhenMemberUnassignedOpen(t *testing.T) {
	store, root, _ := seedDoneWorkflow(t, "open")
	_, done, err := workflowInputDone(beadstest.Work(store), root, []string{"gastown__polecat-gc-xxxx"})
	if err != nil {
		t.Fatalf("workflowInputDone: %v", err)
	}
	if done {
		t.Fatal("done = true for an unassigned open member, want false")
	}
}

func TestSkipDoneWorkflowClosesRootAndSteps(t *testing.T) {
	store, root, step := seedDoneWorkflow(t, "closed")
	if err := store.Update(step.ID, beads.UpdateOpts{Status: strPtr("in_progress"), Assignee: strPtr("worker-1")}); err != nil {
		t.Fatalf("update step: %v", err)
	}

	if err := skipDoneWorkflow(store, root.ID); err != nil {
		t.Fatalf("skipDoneWorkflow: %v", err)
	}
	for _, id := range []string{root.ID, step.ID} {
		got, err := store.Get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got.Status != "closed" {
			t.Fatalf("%s status = %q, want closed", id, got.Status)
		}
		if got.Metadata[beadmeta.OutcomeMetadataKey] != beadmeta.OutcomeSkipped {
			t.Fatalf("%s gc.outcome = %q, want skipped", id, got.Metadata[beadmeta.OutcomeMetadataKey])
		}
	}
}

func TestDoHookClaimSkipsWorkflowWhoseInputIsDone(t *testing.T) {
	candidates := []beads.Bead{{
		ID:     "root-1",
		Status: "open",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:          beadmeta.KindWorkflow,
			beadmeta.InputConvoyIDMetadataKey: "convoy-1",
			beadmeta.RoutedToMetadataKey:      "worker",
		},
	}}
	output, err := json.Marshal(candidates)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	claimed := false
	skipped := ""
	drained := false
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) { return string(output), nil },
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			claimed = true
			return beads.Bead{ID: beadID, Assignee: assignee}, true, nil
		},
		InputDone: func(_ context.Context, _ string, _ []string, _ beads.Bead, _ []string) (string, bool, error) {
			return "root-1", true, nil
		},
		SkipDoneWorkflow: func(_ context.Context, _ string, _ []string, rootID string) error {
			skipped = rootID
			return nil
		},
		DrainAck: func(io.Writer) error {
			drained = true
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:     "worker-1",
		RouteTargets: []string{"worker"},
		DrainAck:     true,
		JSON:         true,
	}, ops, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("doHookClaim = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if claimed {
		t.Fatal("claimed a workflow whose input convoy work is already terminal")
	}
	if skipped != "root-1" {
		t.Fatalf("skipped root = %q, want root-1", skipped)
	}
	if !drained {
		t.Fatal("no-work drain not acknowledged after skipping the done workflow")
	}
	if !strings.Contains(stdout.String(), `"reason":"no_work"`) {
		t.Fatalf("stdout = %q, want no_work drain", stdout.String())
	}
}

func TestDoHookClaimSkipsExistingAssignmentWhoseInputIsDone(t *testing.T) {
	// The per-seat treadmill: the seat already holds an in_progress step of a
	// molecule whose work finished elsewhere. The hook must not hand that step
	// back as existing_assignment; it must close the molecule and drain.
	candidates := []beads.Bead{{
		ID:       "step-1",
		Status:   "in_progress",
		Assignee: "worker-1",
		Metadata: map[string]string{
			beadmeta.RootBeadIDMetadataKey:        "root-1",
			beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
			beadmeta.RoutedToMetadataKey:          "worker",
		},
	}}
	output, err := json.Marshal(candidates)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	skipped := ""
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) { return string(output), nil },
		Claim: func(_ context.Context, _ string, _ []string, beadID, _ string) (beads.Bead, bool, error) {
			t.Fatalf("unexpected claim of %s", beadID)
			return beads.Bead{}, false, nil
		},
		InputDone: func(_ context.Context, _ string, _ []string, _ beads.Bead, _ []string) (string, bool, error) {
			return "root-1", true, nil
		},
		SkipDoneWorkflow: func(_ context.Context, _ string, _ []string, rootID string) error {
			skipped = rootID
			return nil
		},
		DrainAck: func(io.Writer) error { return nil },
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:     "worker-1",
		RouteTargets: []string{"worker"},
		DrainAck:     true,
		JSON:         true,
	}, ops, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("doHookClaim = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if skipped != "root-1" {
		t.Fatalf("skipped root = %q, want root-1", skipped)
	}
	if strings.Contains(stdout.String(), "existing_assignment") {
		t.Fatalf("stdout = %q, handed back a step of a done workflow", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"reason":"no_work"`) {
		t.Fatalf("stdout = %q, want no_work drain", stdout.String())
	}
}

func TestDoHookClaimStillClaimsWhenInputNotDone(t *testing.T) {
	candidates := []beads.Bead{{
		ID:     "root-1",
		Status: "open",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:          beadmeta.KindWorkflow,
			beadmeta.InputConvoyIDMetadataKey: "convoy-1",
			beadmeta.RoutedToMetadataKey:      "worker",
		},
	}}
	output, err := json.Marshal(candidates)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	claimedID := ""
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) { return string(output), nil },
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			claimedID = beadID
			return beads.Bead{ID: beadID, Assignee: assignee, Status: "in_progress"}, true, nil
		},
		InputDone: func(_ context.Context, _ string, _ []string, _ beads.Bead, _ []string) (string, bool, error) {
			return "root-1", false, nil
		},
		SkipDoneWorkflow: func(_ context.Context, _ string, _ []string, rootID string) error {
			t.Fatalf("unexpected skip of %s", rootID)
			return nil
		},
		ListContinuation: func(_ context.Context, _ string, _ []string, _, _ string) ([]beads.Bead, error) {
			return nil, nil
		},
		AssignContinuation: func(_ context.Context, _ string, _ []string, _, _ string) error { return nil },
		ResolveWorkBranch:  func(hookClaimWorkTree) string { return "" },
		StampWorkMeta: func(_ context.Context, _ string, _ []string, _, _ string, _ map[string]string) error {
			return nil
		},
		PublishRunMap: func(_, _ string, _ ...string) error { return nil },
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:     "worker-1",
		RouteTargets: []string{"worker"},
		JSON:         true,
	}, ops, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("doHookClaim = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if claimedID != "root-1" {
		t.Fatalf("claimed = %q, want root-1", claimedID)
	}
}

// TestWorkflowOutlivesTheConvoyItsDrainScatters is the decompose-then-drain
// workflow shape: the run is slung on a source bead (its input convoy tracks
// that bead), a decompose step mints an implementation convoy mid-run, a drain
// fans that convoy out, and gate and review steps run after the drain.
//
// The drain must scatter the implementation convoy, and the run must survive
// that convoy closing: the claim hook retires a workflow once its input
// convoy's work is terminal, and convoy autoclose reaps a root through the
// same key. A pack that re-pointed gc.input_convoy_id at the implementation
// convoy so the drain could find it had every step after the drain closed
// skipped ("input convoy work already terminal") the moment the last task
// closed. gc.drain_convoy_id names the convoy to drain without touching the
// input, so the run lives until the source work itself is done.
func TestWorkflowOutlivesTheConvoyItsDrainScatters(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	formulaDir := t.TempDir()
	itemFormula := "formula = \"drain-item\"\nversion = 1\ncontract = \"graph.v2\"\ntype = \"workflow\"\n\n[[steps]]\nid = \"work\"\ntitle = \"Work {{convoy_id}}\"\n"
	if err := os.WriteFile(filepath.Join(formulaDir, "drain-item.formula.toml"), []byte(itemFormula), 0o644); err != nil {
		t.Fatalf("write drain item formula: %v", err)
	}
	store := beads.NewMemStore()
	mustCreate := func(b beads.Bead) beads.Bead {
		t.Helper()
		created, err := store.Create(b)
		if err != nil {
			t.Fatalf("create %q: %v", b.Title, err)
		}
		return created
	}
	track := func(convoyID, itemID string) {
		t.Helper()
		if err := convoy.TrackItemIn(convoy.MemberClasses{Convoy: beadstest.Work(store)}, convoyID, itemID); err != nil {
			t.Fatalf("track %s in %s: %v", itemID, convoyID, err)
		}
	}
	closeBead := func(id string) {
		t.Helper()
		if err := store.Close(id); err != nil {
			t.Fatalf("close %s: %v", id, err)
		}
	}

	source := mustCreate(beads.Bead{Title: "the change", Type: "task"})
	input := mustCreate(beads.Bead{Title: "input convoy", Type: "convoy", Metadata: map[string]string{beadmeta.SyntheticMetadataKey: "true"}})
	track(input.ID, source.ID)
	root := mustCreate(beads.Bead{Title: "deliver", Type: "task", Metadata: map[string]string{
		beadmeta.KindMetadataKey:            beadmeta.KindWorkflow,
		beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
		beadmeta.InputConvoyIDMetadataKey:   input.ID,
		beadmeta.RoutedToMetadataKey:        "rig/worker",
	}})

	// Decompose: mint the implementation convoy and point the drain at it.
	implementation := mustCreate(beads.Bead{Title: "implementation", Type: "convoy"})
	var taskIDs []string
	for _, title := range []string{"task one", "task two"} {
		task := mustCreate(beads.Bead{Title: title, Type: "task"})
		track(implementation.ID, task.ID)
		taskIDs = append(taskIDs, task.ID)
	}
	if err := store.SetMetadata(root.ID, beadmeta.DrainConvoyIDMetadataKey, implementation.ID); err != nil {
		t.Fatalf("set gc.drain_convoy_id: %v", err)
	}

	drain := mustCreate(beads.Bead{Title: "implement", Type: "task", Metadata: map[string]string{
		beadmeta.KindMetadataKey:       "drain",
		beadmeta.RootBeadIDMetadataKey: root.ID,
		"gc.drain_context":             "separate",
		"gc.drain_formula":             "drain-item",
		"gc.drain_member_access":       "read",
	}})
	gate := mustCreate(beads.Bead{Title: "gate", Type: "task", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey: root.ID,
		beadmeta.RoutedToMetadataKey:   "rig/worker",
	}})

	if _, err := dispatch.ProcessControl(beadstest.Graph(store), drain, dispatch.ProcessOptions{
		Work:               beadstest.Work(store),
		FormulaSearchPaths: []string{formulaDir},
	}); err != nil {
		t.Fatalf("ProcessControl(drain): %v", err)
	}
	var drained []string
	for _, row := range parseDrainManifestRows(t, beadByID(t, store, drain.ID).Metadata[beadmeta.DrainManifestMetadataKey]) {
		drained = append(drained, row.MemberID)
	}
	slices.Sort(drained)
	slices.Sort(taskIDs)
	if !slices.Equal(drained, taskIDs) {
		t.Fatalf("drain scattered %v, want the implementation tasks %v (source %s)", drained, taskIDs, source.ID)
	}

	// Implementation lands: every task closes and the convoy autocloses.
	for _, id := range taskIDs {
		closeBead(id)
	}
	closeBead(implementation.ID)

	if _, done, err := workflowInputDone(beadstest.Work(store), gate, nil); err != nil {
		t.Fatalf("workflowInputDone(gate): %v", err)
	} else if done {
		t.Fatal("the claim hook would retire the workflow once its drained convoy closed; the gate and every later step would be skipped")
	}
	for _, reaped := range collectInputConvoyWorkflowRoots(store, store, beadByID(t, store, taskIDs[0]), map[string]bool{}) {
		if reaped.ID == root.ID {
			t.Fatalf("convoy autoclose would reap workflow %s when an implementation task closed", root.ID)
		}
	}

	// The source work finishing still retires the run, as before.
	closeBead(source.ID)
	if _, done, err := workflowInputDone(beadstest.Work(store), gate, nil); err != nil {
		t.Fatalf("workflowInputDone(gate) after the source closed: %v", err)
	} else if !done {
		t.Fatal("workflowInputDone = false after the source work closed, want true")
	}
}

// TestOldPackRepointingInputConvoyStillRetiresWorkflowEarly pins the failure
// shape the engine fix alone does not cure. A pack that still re-points the
// workflow root's gc.input_convoy_id at the convoy it minted mid-run (instead of
// setting gc.drain_convoy_id) gets its drain through the fallback, but the claim
// hook then reads that convoy as the work the workflow serves: once its tasks
// close, workflowInputDone reports the workflow done while the gate after the
// drain is still open, so the hook retires the run and skips every later step
// (kr-5dq). The pack must stop re-pointing the input key; this test records why.
func TestOldPackRepointingInputConvoyStillRetiresWorkflowEarly(t *testing.T) {
	store := beads.NewMemStore()
	mustCreate := func(b beads.Bead) beads.Bead {
		t.Helper()
		created, err := store.Create(b)
		if err != nil {
			t.Fatalf("create %q: %v", b.Title, err)
		}
		return created
	}
	track := func(convoyID, itemID string) {
		t.Helper()
		if err := convoy.TrackItemIn(convoy.MemberClasses{Convoy: beadstest.Work(store)}, convoyID, itemID); err != nil {
			t.Fatalf("track %s in %s: %v", itemID, convoyID, err)
		}
	}

	source := mustCreate(beads.Bead{Title: "the change", Type: "task"})
	input := mustCreate(beads.Bead{Title: "input convoy", Type: "convoy", Metadata: map[string]string{beadmeta.SyntheticMetadataKey: "true"}})
	track(input.ID, source.ID)
	root := mustCreate(beads.Bead{Title: "deliver", Type: "task", Metadata: map[string]string{
		beadmeta.KindMetadataKey:            beadmeta.KindWorkflow,
		beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
		beadmeta.InputConvoyIDMetadataKey:   input.ID,
		beadmeta.RoutedToMetadataKey:        "rig/worker",
	}})

	// The old pack's decompose: mint the implementation convoy and re-point
	// the input key at it so the drain can find it.
	implementation := mustCreate(beads.Bead{Title: "implementation", Type: "convoy"})
	var taskIDs []string
	for _, title := range []string{"task one", "task two"} {
		task := mustCreate(beads.Bead{Title: title, Type: "task"})
		track(implementation.ID, task.ID)
		taskIDs = append(taskIDs, task.ID)
	}
	if err := store.SetMetadata(root.ID, beadmeta.InputConvoyIDMetadataKey, implementation.ID); err != nil {
		t.Fatalf("re-point gc.input_convoy_id: %v", err)
	}
	gate := mustCreate(beads.Bead{Title: "gate", Type: "task", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey: root.ID,
		beadmeta.RoutedToMetadataKey:   "rig/worker",
	}})

	for _, id := range taskIDs {
		if err := store.Close(id); err != nil {
			t.Fatalf("close %s: %v", id, err)
		}
	}

	if got := beadByID(t, store, gate.ID).Status; got != "open" {
		t.Fatalf("gate status = %q, want open: the step after the drain has not run", got)
	}
	if got := beadByID(t, store, source.ID).Status; got != "open" {
		t.Fatalf("source status = %q, want open: the work served is not done", got)
	}
	if _, done, err := workflowInputDone(beadstest.Work(store), gate, nil); err != nil {
		t.Fatalf("workflowInputDone(gate): %v", err)
	} else if !done {
		t.Fatal("workflowInputDone = false; the old pack's re-pointed input convoy no longer retires the run early, so this failure shape is gone and the test should be revisited")
	}
}
