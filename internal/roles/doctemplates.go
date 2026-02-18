package roles

// PRDTemplate is the scaffolded docs/prd.md for new projects.
// Sections contain HTML comment prompts that agents replace with content.
const PRDTemplate = `# {{project_name}} PRD

The "why" document. Problem statement, users, success criteria, journeys. Hard cap: 5000 lines.

---

## 1. Problem Statement

### 1.1 The Problem
<!-- What pain exists today? Who feels it? Why hasn't it been solved? -->

### 1.2 Why Now
<!-- What changed to make this the right time? -->

---

## 2. User

### 2.1 Primary User
<!-- Who is this for? Technical proficiency, domain, environment. -->

### 2.2 Secondary Users (Future)
<!-- Who might use this later? Not a priority for MVP. -->

---

## 3. Success Criteria

### 3.1 Core Success
<!-- 3-5 concrete conditions that define "this worked." -->

### 3.2 Measurable Checks
<!-- Observable, testable validation criteria. -->

---

## 4. Non-Goals
<!-- Things this project explicitly does not do. Important for scope control. -->

---

## 5. User Journeys
<!-- Step-by-step scenarios showing actual CLI usage or user interaction.
     Include expected output. Make them concrete, not abstract. -->

---

## 6. Risks
<!-- What could go wrong? Mitigation for each. -->

---

## 7. Scope Boundaries

### 7.1 MVP Scope (Build This)
### 7.2 Post-MVP (Build Later, If Needed)
### 7.3 Never Build
`

// SpecTemplate is the scaffolded docs/spec.md for new projects.
const SpecTemplate = `# {{project_name}} Spec

The "what" document. Requirements, behaviors, acceptance criteria. Hard cap: 5000 lines.

---

## 1. Core Model
<!-- What is the fundamental abstraction? How does the system work at the highest level? -->

---

## 2. Components
<!-- What are the major pieces? What does each one do? Define boundaries. -->

---

## 3. Behaviors
<!-- What does the system do in response to user actions? Input -> Output for each. -->

---

## 4. Data Model
<!-- What data exists? Where does it live? What format? Schema if applicable. -->

---

## 5. Constraints
<!-- Hard limits, invariants, things that must always be true. -->
`

// SystemDesignTemplate is the scaffolded docs/systemdesign.md for new projects.
const SystemDesignTemplate = `# {{project_name}} System Design

The "how" document. Architecture, packages, interfaces, build order. Hard cap: 5000 lines.

---

## 1. Module Structure
<!-- Package layout, dependency graph, interface boundaries. -->

---

## 2. Data Structures
<!-- Key types, config format, storage format. -->

---

## 3. Core Algorithms
<!-- Non-obvious logic. Template rendering, state machines, coordination. -->

---

## 4. Command Implementations
<!-- For each command: flow, inputs, outputs, error cases. -->

---

## 5. Testing Strategy
<!-- What gets tested, how, what the test boundaries are. -->

---

## 6. Build Order
<!-- What to build first, dependency chain, parallelizable work. -->
`

// RoadmapTemplate is the scaffolded docs/roadmap.md for new projects.
// This is the only document template with pre-filled content because the
// discovery and design phase is the same for every project.
const RoadmapTemplate = `# {{project_name}} Roadmap

Strategic sequencing: milestones, phases, success gates. Hard cap: 5000 lines.

Beads handles the tactical layer (what's ready, who's assigned, what's blocked). This document captures the strategic layer.

---

## 1. Phases

### Phase 0: Discovery and Design

**Goal:** All four project documents are written and reviewed. The team has a shared understanding of what to build, why, how, and in what order.

**Work:**
1. PM writes docs/prd.md (problem, users, success criteria, journeys)
2. Super orchestrates spec discovery (survey existing patterns, define behaviors)
3. Arch writes docs/systemdesign.md (architecture, packages, interfaces, build order)
4. Super writes docs/roadmap.md phases 1+ (milestones, gates, agent allocation)

**Success gate:** Nelson reviews all four documents. Team can answer: what are we building, why, how, and what ships first?

**Beads:** Create one epic for Phase 0. Four beads: Write PRD, Write Spec, Write System Design, Write Roadmap.

### Phase 1: [First Implementation Milestone]

**Goal:**
**Packages to build:**
**Success gate:**
**Beads:**

---

## 2. Milestone Summary

---

## 3. Agent Allocation

---

## 4. Risk Gates
`
