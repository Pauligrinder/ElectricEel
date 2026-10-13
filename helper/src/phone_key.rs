//! Pure application policy; no Go state machine or wall-clock comparisons.
use std::time::{Duration, Instant};

pub(crate) struct Proximity {
    pub near: bool,
    confirmations: u32,
    last_seen: Option<Instant>,
    pub target: u64,
    pub target_at: Option<Instant>,
    pub retry_at: Option<Instant>,
    backoff: Duration,
}

impl Default for Proximity {
    fn default() -> Self {
        Self {
            near: false,
            confirmations: 0,
            last_seen: None,
            target: 0,
            target_at: None,
            retry_at: None,
            backoff: Duration::ZERO,
        }
    }
}

impl Proximity {
    pub fn advertisement(&mut self, now: Instant, target: u64, rssi: i32) {
        if rssi >= -90 {
            self.confirmations += 1;
            self.last_seen = Some(now);
            self.target = target;
            self.target_at = Some(now);
            if self.confirmations >= 1 {
                self.near = true;
            }
        } else {
            self.confirmations = 0;
            self.target = 0;
            self.target_at = None;
        }
    }
    pub fn tick(&mut self, now: Instant, connected: bool) -> bool {
        if connected {
            self.near = true;
            self.last_seen = Some(now);
            return false;
        }
        if self.near
            && self
                .last_seen
                .is_some_and(|seen| now.duration_since(seen) > Duration::from_secs(15))
        {
            self.near = false;
            self.target = 0;
            self.target_at = None;
            self.retry_at = None;
            self.backoff = Duration::ZERO;
            return true;
        }
        false
    }
    pub fn can_connect(&self, now: Instant) -> bool {
        self.near
            && self.target != 0
            && self
                .target_at
                .is_some_and(|seen| now.duration_since(seen) <= Duration::from_secs(4))
            && self.retry_at.is_none_or(|deadline| now >= deadline)
    }
    pub fn failed(&mut self, now: Instant) {
        self.backoff = (self.backoff * 2)
            .max(Duration::from_secs(1))
            .min(Duration::from_secs(60));
        self.retry_at = Some(now + self.backoff);
    }
    pub fn connected(&mut self) {
        self.backoff = Duration::ZERO;
        self.retry_at = None;
    }
}

pub(crate) fn inside(connected: Instant, status_at: Instant, now: Instant, present: bool) -> bool {
    present
        && now.duration_since(connected) >= Duration::from_secs(45)
        && now.duration_since(status_at) <= Duration::from_secs(30)
}

/// A single runtime-owned deadline, independent of Automagic flow concurrency.
/// Raw `presence_far` remains immediate. Only `presence_inside` cancels a departure
/// within a session; stopped/restarted sessions and VIN changes discard it.
#[derive(Default)]
pub(crate) struct AwayDeadline {
    pending: Option<PendingAway>,
}

struct PendingAway {
    deadline: Instant,
    vin: String,
    generation: u64,
}

impl AwayDeadline {
    pub(crate) fn observe(&mut self, kind: &str, vin: &str, generation: u64, now: Instant) {
        self.discard_other_session(vin, generation);
        match kind {
            "presence_far" if self.pending.is_none() => {
                self.pending = Some(PendingAway {
                    deadline: now + Duration::from_secs(180),
                    vin: vin.into(),
                    generation,
                });
            }
            "presence_inside" | "presence_stopped" | "presence_restarted" => self.pending = None,
            _ => (),
        }
    }

    pub(crate) fn deadline(&self) -> Option<Instant> {
        self.pending.as_ref().map(|away| away.deadline)
    }

    pub(crate) fn take_due(&mut self, now: Instant, vin: &str, generation: u64) -> Option<String> {
        self.discard_other_session(vin, generation);
        if self.deadline().is_some_and(|deadline| now >= deadline) {
            return self.pending.take().map(|away| away.vin);
        }
        None
    }

    fn discard_other_session(&mut self, vin: &str, generation: u64) {
        if self
            .pending
            .as_ref()
            .is_some_and(|away| away.vin != vin || away.generation != generation)
        {
            self.pending = None;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn connected_link_survives_missing_advertisements_and_stale_target_cannot_reconnect() {
        let now = Instant::now();
        let mut state = Proximity::default();
        state.advertisement(now, 1, -80);
        assert!(state.can_connect(now));
        assert!(!state.tick(now + Duration::from_secs(60), true));
        assert!(state.near);
        assert!(!state.can_connect(now + Duration::from_secs(60)));
        assert!(state.tick(now + Duration::from_secs(76), false));
        assert!(!state.near);
    }
    #[test]
    fn weak_samples_and_failures_do_not_spin_reconnects() {
        let now = Instant::now();
        let mut state = Proximity::default();
        state.advertisement(now, 1, -100);
        assert!(!state.can_connect(now));
        state.advertisement(now, 2, -80);
        state.failed(now);
        assert!(!state.can_connect(now));
        assert!(state.can_connect(now + Duration::from_secs(1)));
        state.advertisement(now + Duration::from_secs(2), 3, -100);
        assert!(!state.can_connect(now + Duration::from_secs(2)));
    }

    #[test]
    fn inside_requires_settled_connection_and_fresh_positive_vehicle_report() {
        let connected = Instant::now();
        let now = connected + Duration::from_secs(46);
        assert!(inside(connected, now, now, true));
        assert!(!inside(connected, now, now, false));
        assert!(!inside(now, now, now, true));
        assert!(!inside(connected, connected, now, true));
    }

    #[test]
    fn repeated_departures_keep_first_deadline_and_expire_only_once() {
        let now = Instant::now();
        let mut away = AwayDeadline::default();
        away.observe("presence_far", "VIN", 1, now);
        away.observe("presence_far", "VIN", 1, now + Duration::from_secs(120));
        assert!(away
            .take_due(now + Duration::from_secs(179), "VIN", 1)
            .is_none());
        assert_eq!(
            away.take_due(now + Duration::from_secs(180), "VIN", 1),
            Some("VIN".into())
        );
        assert!(away
            .take_due(now + Duration::from_secs(300), "VIN", 1)
            .is_none());
    }

    #[test]
    fn inside_at_deadline_cancels_and_next_departure_gets_a_fresh_timer() {
        let now = Instant::now();
        let mut away = AwayDeadline::default();
        away.observe("presence_far", "VIN", 1, now);
        let later = now + Duration::from_secs(180);
        away.observe("presence_inside", "VIN", 1, later);
        assert!(away.take_due(later, "VIN", 1).is_none());
        away.observe("presence_far", "VIN", 1, later);
        assert!(away
            .take_due(later + Duration::from_secs(179), "VIN", 1)
            .is_none());
        assert_eq!(
            away.take_due(later + Duration::from_secs(180), "VIN", 1),
            Some("VIN".into())
        );
    }

    #[test]
    fn only_inside_cancels_within_session_but_stops_and_identity_changes_discard_timer() {
        let now = Instant::now();
        let later = now + Duration::from_secs(180);
        for kind in [
            "presence_near",
            "presence_auth_ok",
            "presence_disconnected",
            "presence_error",
        ] {
            let mut away = AwayDeadline::default();
            away.observe("presence_far", "VIN", 1, now);
            away.observe(kind, "VIN", 1, now);
            assert_eq!(away.take_due(later, "VIN", 1), Some("VIN".into()));
        }
        for kind in ["presence_stopped", "presence_restarted"] {
            let mut away = AwayDeadline::default();
            away.observe("presence_far", "VIN", 1, now);
            away.observe(kind, "VIN", 1, now);
            assert!(away.take_due(later, "VIN", 1).is_none());
        }
        for (vin, generation) in [("OTHER", 1), ("VIN", 2)] {
            let mut away = AwayDeadline::default();
            away.observe("presence_far", "VIN", 1, now);
            assert!(away.take_due(later, vin, generation).is_none());
            assert!(away.take_due(later, "VIN", 1).is_none());
        }
    }
}
