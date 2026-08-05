---
name: Trust-surface feedback (5 minutes)
about: First impressions of `graphi trust-report` — your answers are real usability evidence for the P1 trust surface
title: "trust-surface feedback: "
labels: ["trust-surface", "usability"]
---

<!-- Thanks! graphi's trust surface (Labs) is evaluated with real first-time
     users, not simulations. Five minutes and four questions — honest
     confusion is MORE valuable than a guessed right answer. -->

**Setup (once):** in a repo of yours, run `graphi sync`, then `graphi trust-report`
and `graphi status`. Optionally: `graphi trust-report --policy automated_change`
and `graphi trust-report --target <some file>`.

### 1. In your own words: what does the trust report tell you about this repository's graph — and how far would you trust query answers based on it?

<!-- your answer -->

### 2. The same repo has a `status` output and a `trust-report` output. What is the difference between what these two commands answer?

<!-- your answer -->

### 3. Scenario: you want an AI agent to perform an UNATTENDED automated refactor. Which `--policy` do you pass, what does the verdict you got mean, and what would you do next?

<!-- your answer -->

### 4. If you ran `--target`: what does that output tell you about the specific file/symbol, as opposed to the whole repository?

<!-- your answer -->

### What confused you?

<!-- anything unclear, surprising, or misleading — this is the most useful part -->
