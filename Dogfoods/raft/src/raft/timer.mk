// Why: elections and heartbeats run on timers the simulator controls.
// What: Timer and TimerKind.
// How: a timer is a deadline in simulated milliseconds and its kind.

enum TimerKind: u8 {
    Election = 0
    Heartbeat = 1
}

// A timer fires at `at` unless its kind's generation has moved past its own: a
// reset pushes a new timer instead of finding the old one in the heap.
struct Timer {
    at: i64
    kind: TimerKind
    generation: i64
}

// Earlier first; of two at one instant, the older generation first, so the
// order never depends on the heap's layout.
impl Ord for Timer {
    Less(&self, other: &Timer): bool {
        if self.at != other.at {
            return self.at < other.at
        }
        return self.generation < other.generation
    }
}
