package training

import (
	"github.com/huggan360/plainshow-cluster/internal/store"
	"strings"
	"testing"
)

func TestGangReservation(t *testing.T) {
	r := NewReservations()
	if err := r.Reserve("a", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Reserve("b", []string{"two", "three"}); err == nil {
		t.Fatal("partial overlapping gang accepted")
	}
	r.Release("a")
	if err := r.Reserve("b", []string{"two", "three"}); err != nil {
		t.Fatal(err)
	}
}
func TestTorchPlan(t *testing.T) {
	nodes := []store.NetworkNode{{NodeID: "a", Address: "https://10.0.0.1:10000"}, {NodeID: "b", Address: "https://10.0.0.2:10000"}}
	plan, err := Build("run", "pytorch", "train.py --epochs 2", 1, nodes, "/checkpoints")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ranks) != 2 || !strings.Contains(plan.Ranks[1].Command, "--node-rank=1") || plan.Ranks[0].Environment["MASTER_ADDR"] != "10.0.0.1" {
		t.Fatal(plan)
	}
}
func TestAdvisorIsHonest(t *testing.T) {
	advice := Advise(1_000_000_000, 4, 100, 1, 2)
	if advice.CommunicationPercent < 99 || !strings.Contains(advice.Verdict, "dominates") {
		t.Fatal(advice)
	}
}
