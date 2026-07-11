# Scenario: bad-deploy

**Trigger:** Deployment of checkout v2.3.1 at 14:20:00  
**Expected top hypothesis:** RW001 (Deploy → latency spike)  
**Expected confidence:** High  

## Timeline
- 14:20:00 Deploy checkout v2.3.1
- 14:20:40 latency.p99 ↑4.2× (baseline ~40ms → ~180ms)
- 14:21:15 OOMKill checkout-7d9f
- 14:21:18 CrashLoop (≥3 restarts)
- 14:22:00 error.rate ↑8.1×

## Verdict assertions (Phase 4)
- Hypotheses[0].TriggerEventID points to the Deploy event
- Hypotheses[0].Confidence == "high"
- Hypotheses[0].RuleIDs contains "RW001"
- Hypotheses[0].RuleIDs contains "RW003"
- No High-confidence verdict on the false-positive scenario
