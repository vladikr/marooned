package pool

import (
	"sync"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	virtv1 "kubevirt.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/util"
)

func poolVMI(name, node, class, tee, state string, rv string) *virtv1.VirtualMachineInstance {
	return &virtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       util.DefaultInfraNamespace,
			ResourceVersion: rv,
			Labels: map[string]string{
				util.SandboxModeLabel:      util.SandboxModeSandbox,
				util.SandboxNodeLabel:      node,
				util.SandboxSizeClassLabel: class,
				util.SandboxTEELabel:       tee,
				util.WarmPoolStateLabel:    state,
			},
		},
		Status: virtv1.VirtualMachineInstanceStatus{Phase: virtv1.Running},
	}
}

func TestSelectAvailableMatchesKey(t *testing.T) {
	vmis := []*virtv1.VirtualMachineInstance{
		poolVMI("a", "n1", "s", "off", util.PoolStateAvailable, "1"),
		poolVMI("b", "n1", "m", "off", util.PoolStateAvailable, "1"),
		poolVMI("c", "n2", "s", "off", util.PoolStateAvailable, "1"),
		poolVMI("d", "n1", "s", "snp", util.PoolStateAvailable, "1"),
		poolVMI("e", "n1", "s", "off", util.PoolStateClaimed, "1"),
	}
	got := SelectAvailable(vmis, Key{Node: "n1", SizeClass: "s", TEE: "off"})
	if got == nil || got.Name != "a" {
		t.Fatalf("got %+v", got)
	}
	if SelectAvailable(vmis, Key{Node: "n1", SizeClass: "s", TEE: "tdx"}) != nil {
		t.Fatal("must not cross-claim tdx")
	}
	if SelectAvailable(vmis, Key{Node: "n1", SizeClass: "s", TEE: "snp"}).Name != "d" {
		t.Fatal("snp pool")
	}
}

func TestClaimRaceTwoPodsOneVMI(t *testing.T) {
	store := map[string]*virtv1.VirtualMachineInstance{
		"a": poolVMI("a", "n1", "s", "off", util.PoolStateAvailable, "1"),
	}
	var mu sync.Mutex
	var conflicts int32

	claim := func(pod string) bool {
		mu.Lock()
		cur := store["a"].DeepCopy()
		mu.Unlock()
		if cur.Labels[util.WarmPoolStateLabel] != util.PoolStateAvailable {
			return false
		}
		next := MarkClaimed(cur, pod, "sb-"+pod)
		mu.Lock()
		defer mu.Unlock()
		if store["a"].ResourceVersion != next.ResourceVersion {
			atomic.AddInt32(&conflicts, 1)
			return false
		}
		if store["a"].Labels[util.WarmPoolStateLabel] != util.PoolStateAvailable {
			atomic.AddInt32(&conflicts, 1)
			return false
		}
		next.ResourceVersion = "2"
		store["a"] = next
		return true
	}

	var wg sync.WaitGroup
	var wins int32
	for _, pod := range []string{"app/p1", "app/p2"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			if claim(p) {
				atomic.AddInt32(&wins, 1)
			}
		}(pod)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("expected exactly one winner, got %d conflicts=%d", wins, conflicts)
	}
	if store["a"].Labels[util.WarmPoolStateLabel] != util.PoolStateClaimed {
		t.Fatal("not claimed")
	}
}

func TestDeficit(t *testing.T) {
	if Deficit(1, 1, 2) != 0 {
		t.Fatal("have enough")
	}
	if Deficit(0, 0, 2) != 2 {
		t.Fatal("need 2")
	}
}

func TestPVCPodDoesNotDecrementAvailablePool(t *testing.T) {
	available := []*virtv1.VirtualMachineInstance{
		poolVMI("a", "n1", "s", "off", util.PoolStateAvailable, "1"),
		poolVMI("b", "n1", "s", "off", util.PoolStateAvailable, "1"),
	}
	idx := Index(available)
	key := Key{Node: "n1", SizeClass: "s", TEE: "off"}
	before := len(idx[key][util.PoolStateAvailable])

	pvc := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "isolated-pvc",
			Namespace:   "app",
			UID:         "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			Annotations: map[string]string{util.PlacementAnnotation: util.PlacementUser},
		},
	}
	plan := sandbox.PlanVMI(pvc, util.DefaultInfraNamespace)
	if plan.ClaimPool {
		t.Fatal("PVC pod must not claim")
	}
	// Adaptor never calls MarkClaimed when ClaimPool is false.
	idxAfter := Index(available)
	if len(idxAfter[key][util.PoolStateAvailable]) != before {
		t.Fatalf("pool available changed from %d to %d", before, len(idxAfter[key][util.PoolStateAvailable]))
	}
	inNs := &virtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandbox.UserNamespaceVMIName(pvc.UID),
			Namespace: "app",
			Labels: map[string]string{
				util.SandboxModeLabel: util.SandboxModeSandbox,
			},
		},
	}
	withInNs := append(available, inNs)
	if len(Index(withInNs)[key][util.PoolStateAvailable]) != before {
		t.Fatal("in-ns VMI must not be indexed as pool")
	}
}

func TestMarkAvailableClearsClaim(t *testing.T) {
	vmi := poolVMI("a", "n1", "s", "off", util.PoolStateClaimed, "3")
	vmi.Labels[util.WarmPoolClaimedByLabel] = "app/p"
	out := MarkAvailable(vmi)
	if out.Labels[util.WarmPoolStateLabel] != util.PoolStateAvailable {
		t.Fatal("state")
	}
	if _, ok := out.Labels[util.WarmPoolClaimedByLabel]; ok {
		t.Fatal("claimed-by still set")
	}
}
