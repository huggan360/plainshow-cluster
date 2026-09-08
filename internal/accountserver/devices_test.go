package accountserver

import (
	"errors"
	"testing"
)

// twoAccounts sets up a bootstrapped store with two people and one network
// owned by the first.
func twoAccounts(t *testing.T) (*Store, Account, Account) {
	t.Helper()
	store := openTestStore(t)
	if err := store.InitialiseBootstrap(TokenHash("secret")); err != nil {
		t.Fatal(err)
	}
	hugo := Account{ID: "a1", Username: "huggan360", DisplayName: "Hugo", PasswordHash: "h"}
	if err := store.CreateAccount(hugo, TokenHash("secret"), false); err != nil {
		t.Fatal(err)
	}
	albin := Account{ID: "a2", Username: "albin", DisplayName: "Albin", PasswordHash: "h"}
	if err := store.CreateAccount(albin, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterNetwork(hugo.ID, NetworkRegistration{
		ID: "net-lab", Name: "Research lab", ManagementKey: testKey, Role: "owner",
	}); err != nil {
		t.Fatal(err)
	}
	return store, hugo, albin
}

const testKey = "0123456789abcdef0123456789abcdef0123456789"

func TestDevicesAreScopedToTheirOwner(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	if _, err := store.CheckIn(hugo.ID, NodeCheckIn{ID: "n1", Name: "Stationary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CheckIn(albin.ID, NodeCheckIn{ID: "n2", Name: "Albin's box"}); err != nil {
		t.Fatal(err)
	}
	devices, err := store.DevicesForAccount(hugo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Name != "Stationary" {
		t.Fatalf("devices for hugo = %+v, want only his own", devices)
	}
	if !devices[0].Online {
		t.Error("a device that just checked in read as offline")
	}
}

// TestMovingADeviceIsARequestNotAnOrder: the account service records a wish and
// the device carries it out itself on its next check-in. That ordering is what
// keeps a machine's own policy the last word about what happens on it.
func TestMovingADeviceIsARequestNotAnOrder(t *testing.T) {
	store, hugo, _ := twoAccounts(t)
	if _, err := store.CheckIn(hugo.ID, NodeCheckIn{ID: "n1", Name: "Stationary"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeviceNetwork(hugo.ID, "n1", "net-lab"); err != nil {
		t.Fatal(err)
	}

	instructions, err := store.CheckIn(hugo.ID, NodeCheckIn{ID: "n1", Name: "Stationary"})
	if err != nil {
		t.Fatal(err)
	}
	if instructions.DesiredNetwork != "net-lab" {
		t.Fatalf("check-in returned %+v, want a move to net-lab", instructions)
	}

	// Reporting the new network is the acknowledgement. A request that stayed
	// set would show as a pending move that had already happened.
	instructions, err = store.CheckIn(hugo.ID, NodeCheckIn{
		ID: "n1", Name: "Stationary", ActiveNetwork: "net-lab",
	})
	if err != nil {
		t.Fatal(err)
	}
	if instructions.DesiredNetwork != "" {
		t.Errorf("a completed move was still pending: %+v", instructions)
	}
}

func TestMovingADeviceIntoAStrangersNetworkIsRefused(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	if _, err := store.CheckIn(albin.ID, NodeCheckIn{ID: "n2", Name: "Albin's box"}); err != nil {
		t.Fatal(err)
	}
	// Albin owns the device but is not in hugo's network.
	if err := store.SetDeviceNetwork(albin.ID, "n2", "net-lab"); !errors.Is(err, ErrNetworkMember) {
		t.Fatalf("error = %v, want ErrNetworkMember", err)
	}
	// Hugo is in the network but does not own the device.
	if err := store.SetDeviceNetwork(hugo.ID, "n2", "net-lab"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("error = %v, want ErrDeviceNotFound", err)
	}
}

// TestSignOutIsDeliveredOnce: a device that signs out stops checking in, so
// waiting for an acknowledgement that can never arrive would sign it out again
// every time somebody signed back in.
func TestSignOutIsDeliveredOnce(t *testing.T) {
	store, hugo, _ := twoAccounts(t)
	if _, err := store.CheckIn(hugo.ID, NodeCheckIn{ID: "n1", Name: "Stationary"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RequestDeviceSignOut(hugo.ID, "n1"); err != nil {
		t.Fatal(err)
	}
	first, err := store.CheckIn(hugo.ID, NodeCheckIn{ID: "n1", Name: "Stationary"})
	if err != nil || !first.SignOut {
		t.Fatalf("first check-in = %+v, %v, want SignOut", first, err)
	}
	second, err := store.CheckIn(hugo.ID, NodeCheckIn{ID: "n1", Name: "Stationary"})
	if err != nil || second.SignOut {
		t.Fatalf("second check-in = %+v, %v, want no repeat", second, err)
	}
}

func TestRemovingSomebodyElsesDeviceIsRefused(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	if _, err := store.CheckIn(albin.ID, NodeCheckIn{ID: "n2", Name: "Albin's box"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveDevice(hugo.ID, "n2"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("error = %v, want ErrDeviceNotFound", err)
	}
}

// TestADeviceReportsWhatItIs backs the device list saying which machine should
// run something. A count of graphics cards cannot answer that; their names,
// vendors and the machine's cores can.
//
// It also guards the migration: the columns are added by migrate() and used by
// the check-in insert, and losing one leaves a query that builds happily and
// fails the moment anything checks in.
func TestADeviceReportsWhatItIs(t *testing.T) {
	store, hugo, _ := twoAccounts(t)
	const cards = `[{"name":"NVIDIA GeForce RTX 5060","vendor":"nvidia","trainable":true,"vram_total_mb":8151}]`

	if _, err := store.CheckIn(hugo.ID, NodeCheckIn{
		ID: "n1", Name: "Stationary", GPUCount: 1,
		CPUCores: 16, RAMTotalMB: 32768, GPUs: cards,
	}); err != nil {
		t.Fatal(err)
	}
	devices, err := store.DevicesForAccount(hugo.ID)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices = %+v, %v", devices, err)
	}
	if devices[0].CPUCores != 16 || devices[0].RAMTotalMB != 32768 {
		t.Errorf("machine reported as %+v", devices[0])
	}
	if string(devices[0].GPUs) != cards {
		t.Errorf("gpus = %s", devices[0].GPUs)
	}

	// A machine that reports nothing must come back as an empty list, not a
	// blank the browser has to guard against.
	if _, err := store.CheckIn(hugo.ID, NodeCheckIn{ID: "n2", Name: "Headless"}); err != nil {
		t.Fatal(err)
	}
	devices, _ = store.DevicesForAccount(hugo.ID)
	for _, device := range devices {
		if len(device.GPUs) == 0 || string(device.GPUs) == "" {
			t.Errorf("%s reported gpus as blank rather than []", device.Name)
		}
	}
}
