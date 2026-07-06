package main

// XHS risk-control cooldown — the SERVER-SIDE circuit breaker (W-02).
//
// The playbooks (xiaohongshu_search_playbook / source_health_and_degradation) mandate STOP-on-
// risk-signal: on ANY EOF / 扫码 / 验证码 / 操作频繁 / empty-after-success, the source must cool down,
// never retry-until-it-works (狂刷 = escalating a soft limit into an irreversible hard ban).
//
// This is the ONLY layer that protects workflow sub-agents that call mcp__xiaohongshu-mcp__*
// DIRECTLY — they bypass the ROS bridge (ros/lib/social_pacing.py), which only covers `ros xhs`.
// File-backed under $ROS_SOCIAL_HOME (the same state dir the daemon script + ros.lib.social_pacing
// use) so the cooldown is shared across the MCP server, `ros xhs`, and concurrent sub-agents — a
// signal seen by any one of them stops all of them. Marker matching is pure substring containment
// (validation, not reasoning).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const xhsCooldownDuration = 30 * time.Minute

// xhsRiskMarkers — pure substring match against the JSON-marshaled result. NOT a semantic judgement.
var xhsRiskMarkers = []string{
	"扫码", "请打开 App", "请打开App", "验证码", "滑块", "操作频繁", "操作太频繁", "稍后再试",
	"risk_control", "blocked", "EOF", "panic", "unauthorized",
}

type cooldownRecord struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
	SetAt  time.Time `json:"set_at"`
}

var cooldownMu sync.Mutex

func cooldownFile() string {
	home := os.Getenv("ROS_SOCIAL_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".researchos", "social_mcp")
	}
	_ = os.MkdirAll(home, 0o755)
	return filepath.Join(home, "xhs_cooldown.json")
}

// xhsCooldownActive returns the active record if XHS is currently in cooldown (and lazily clears
// an expired one).
func xhsCooldownActive() (cooldownRecord, bool) {
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	b, err := os.ReadFile(cooldownFile())
	if err != nil {
		return cooldownRecord{}, false
	}
	var rec cooldownRecord
	if json.Unmarshal(b, &rec) != nil {
		return cooldownRecord{}, false
	}
	if time.Now().After(rec.Until) {
		_ = os.Remove(cooldownFile())
		return cooldownRecord{}, false
	}
	return rec, true
}

func xhsSetCooldown(reason string) cooldownRecord {
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	rec := cooldownRecord{Until: time.Now().Add(xhsCooldownDuration), Reason: reason, SetAt: time.Now()}
	if b, err := json.Marshal(rec); err == nil {
		_ = os.WriteFile(cooldownFile(), b, 0o600)
	}
	logrus.Warnf("XHS cooldown set for %v: %s — risk-control signal detected; further dispatch refused",
		xhsCooldownDuration, reason)
	return rec
}

// xhsGateCheck returns an error (surfaced as the MCP result so callers/agents see the STOP signal)
// when XHS is in cooldown. op labels the operation for the message.
func xhsGateCheck(op string) error {
	if rec, active := xhsCooldownActive(); active {
		return fmt.Errorf("XHS %s refused: source in COOLDOWN until %s (%s). A risk-control signal was "+
			"seen on an earlier call; retrying now risks escalating a soft limit into an irreversible "+
			"account ban. Wait for the cooldown to expire (or clear %s).",
			op, rec.Until.Format(time.RFC3339), rec.Reason, cooldownFile())
	}
	return nil
}

// xhsMarkRisk inspects a search/detail result + error for risk-control signals and sets cooldown.
// blob is the JSON-marshaled result (searched for markers). Empty-but-success is NOT auto-flagged
// (a narrow query legitimately returns empty); only errors and marker text trip the breaker.
func xhsMarkRisk(op string, err error, blob string) {
	if err != nil {
		xhsSetCooldown(fmt.Sprintf("%s error: %v", op, err))
		return
	}
	for _, m := range xhsRiskMarkers {
		if strings.Contains(blob, m) {
			xhsSetCooldown(fmt.Sprintf("%s risk marker %q", op, m))
			return
		}
	}
}
