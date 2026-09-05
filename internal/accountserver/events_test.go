package accountserver

import "testing"

// TestWakeUpsGoOnlyToTheAccountThatOwnsThem is the security-relevant part of
// this channel. A wake-up says nothing itself, but knowing that somebody else's
// devices changed is still knowing something, and a routing mistake here would
// be invisible in normal use.
func TestWakeUpsGoOnlyToTheAccountThatOwnsThem(t *testing.T) {
	hub := newWatchers()
	mine := hub.add("a1")
	theirs := hub.add("a2")
	defer hub.remove(mine)
	defer hub.remove(theirs)

	hub.notify("a1", TopicDevices)
	select {
	case topic := <-mine.events:
		if topic != TopicDevices {
			t.Fatalf("topic = %q", topic)
		}
	default:
		t.Fatal("the owning account was not woken")
	}
	select {
	case topic := <-theirs.events:
		t.Fatalf("another account was woken with %q", topic)
	default:
	}
}

// TestASlowDeviceCannotWedgeAWrite: notify runs inside the request that changed
// something, so a device that has stopped reading must be dropped rather than
// allowed to block whoever is signing it out.
func TestASlowDeviceCannotWedgeAWrite(t *testing.T) {
	hub := newWatchers()
	stuck := hub.add("a1")
	defer hub.remove(stuck)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			hub.notify("a1", TopicDevices)
		}
		close(done)
	}()
	<-done // would never close if notify blocked on a full buffer
}

func TestRemovingAWatcherStopsIt(t *testing.T) {
	hub := newWatchers()
	item := hub.add("a1")
	hub.remove(item)
	if _, open := <-item.events; open {
		t.Fatal("a removed watcher is still open")
	}
	// Removing twice is how a defer plus an explicit close meet, and must not
	// close an already closed channel.
	hub.remove(item)
	if hub.count("a1") != 0 {
		t.Fatal("a removed watcher is still counted")
	}
}
