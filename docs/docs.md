# Telegram Mafia / Werewolf Bot — Full Design Document

## 1. Goals & Scope

- Support multiple concurrent games, one per group chat, with no cross-contamination of state.
- Private role assignment and night actions via DM, public deliberation/voting in the group.
- Deterministic, testable game engine (state machine) decoupled from the Telegram transport layer.
- Resilient to disconnects/restarts — game state must survive a bot process restart mid-game.
- Fair, cheat-resistant (players can't see others' roles, can't act out of turn, can't double-vote).

Non-goals (v1): voice chat integration, tournament/ranking system, payments. These can be layered on later.

---

## 2. Core Rules (Base Ruleset)

### 2.1 Roles

Every role is described exactly once, in the `roleCatalog` table in
`internal/engine/roleinfo.go`. Team, emoji, role-DM blurb, night action token,
action prompt, targeting rule, one-shot flag and investigation disguise all
live in that one entry, so adding a role means adding a row rather than editing
a dozen switch statements. The `/roles` command and the role DMs are both
rendered from it, which is what keeps them from drifting apart.

| Role | Team | Ability | Minimum players |
|---|---|---|---|
| Villager | Town | None (vote only) | — (remainder) |
| Mafia (Werewolf) | Mafia | Each night, the mafia collectively choose one player to eliminate | — (~25%, min 1) |
| Detective (Seer) | Town | Each night, learn one player's faction | 6 |
| Doctor | Town | Each night, protect one player from every kill source | 7 |
| Escort | Town | Each night, roleblock one player: whatever they planned does not happen | 8 |
| Lookout | Town | Each night, watch one player and learn who visited them | 8 |
| Bodyguard | Town | Each night, guard one player: takes the blow and kills the attacker | 9 |
| Mayor | Town | May `/reveal` once during the day; their vote then counts as `MayorVoteWeight` | 9 |
| Vigilante | Town | One bullet for the whole game, fired at night | 10 |
| Jester | Neutral | Wins if the town lynches them; the game continues for everyone else | 8 |
| Survivor | Neutral | No power; wins by being alive when the game ends, whoever else won | 10 |
| Serial Killer | Killer | Kills one player every night, alone; reads as Mafia | 11 |
| Godfather | Mafia | Kills as mafia, but reads as Town | 9 (replaces a mafia slot) |
| Framer | Mafia | Each night, plants evidence so one player reads as Mafia that night | 11 (replaces a mafia slot) |

Role counts are generated from the pool in `DefaultOptionalRoles()` keyed on
player count, never hardcoded — see §5.2. A role that joins the mafia must set
`ReplacesMafiaSlot`, otherwise it adds an extra enemy on top of the computed
mafia count and quietly unbalances the game; `TestOptionalMafiaRolesReplaceASlot`
enforces this.

**Lovers** is a modifier rather than a role. When `EnableLovers` is on, two
players are paired at the deal and told about each other. When one dies, the
other dies of grief, whatever their roles were. Grief cascades through the
single `killPlayer` helper, so a pair always leaves together no matter which
death path started it.

### 2.2 Win Conditions

There are three hostile possibilities on the board — the mafia, a lone killer,
and neutrals who win on their own terms — so a faction only takes the game once
every *rival killer* is gone or it holds enough of the board that nothing can
stop it.

- **Town wins** when every mafia member *and* every serial killer is dead.
- **Mafia wins** at parity (mafia ≥ everyone else) **and** no serial killer is
  left alive to take it from them.
- **Serial Killer wins** at parity with no mafia left alive.
- **Jester wins individually** if the town lynches them. The game continues.
- **Survivor wins individually** by being alive at the end, alongside whoever
  won the game itself.
- **No winner** when everybody is dead, or when every remaining player has
  disconnected. This is recorded as an *aborted* game and counts as neither a
  win nor a loss for anyone (§11.1).

Neutral wins are recorded as they qualify and are checked independently of the
faction result, so a Jester lynched on day 2 keeps their win even if the mafia
go on to take the game.

### 2.3 Phases per Game Day

1. **Night Phase** — Mafia vote (DM), Detective checks (DM), Doctor protects (DM). All resolved simultaneously at phase end.
2. **Day Announcement** — Bot posts who died (or "no one died" if Doctor saved them), with cause hidden (role not revealed unless configured otherwise).
3. **Discussion Phase** — Timed open chat in the group. No bot enforcement needed beyond a timer message.
4. **Voting Phase** — Players vote publicly (inline buttons) to lynch one player, or vote "no lynch" if that variant is enabled. Majority (or plurality, configurable) required.
5. **Lynch Resolution** — Announce eliminated player, reveal role (configurable), check win conditions.
6. Loop back to Night Phase, or end game.

---

## 3. State Machine

```
IDLE
  │  /startgame
  ▼
LOBBY (collecting players, min/max enforced)
  │  /begin (host) OR auto-start timer expires with enough players
  ▼
ROLE_ASSIGNMENT (DM roles to all players)
  │  all DMs delivered (or timeout/fallback, see §8.3)
  ▼
NIGHT_ACTION_COLLECTION ◄────────────────────┐
  │  all required actions submitted OR timer expires │
  ▼                                                    │
NIGHT_RESOLUTION (compute deaths, protections, checks) │
  │                                                     │
  ▼                                                     │
WIN_CHECK ──(no winner)──► DAY_ANNOUNCEMENT             │
  │ (winner)                    │                       │
  ▼                             ▼                       │
GAME_OVER               DISCUSSION (timed)              │
                                 │                       │
                                 ▼                       │
                         VOTING (timed, inline buttons)  │
                                 │                       │
                                 ▼                       │
                         LYNCH_RESOLUTION                │
                                 │                       │
                                 ▼                       │
                         WIN_CHECK ──(no winner)─────────┘
                                 │ (winner)
                                 ▼
                            GAME_OVER
```

Each state transition should be an explicit function `(GameState, Event) -> GameState` — a pure reducer, not scattered `if` statements across handlers. This makes the engine unit-testable without touching the Telegram API at all (important, since Mafia has a huge edge-case surface).

---

## 4. Data Model (Go)

```go
type GameID string // typically the Telegram chat ID as string

type PlayerID int64 // Telegram user ID

type Role string

const (
    RoleVillager  Role = "villager"
    RoleMafia     Role = "mafia"
    RoleDetective Role = "detective"
    RoleDoctor    Role = "doctor"
    RoleGodfather Role = "godfather"
    RoleVigilante Role = "vigilante"
    RoleJester    Role = "jester"
)

type Phase string

const (
    PhaseLobby       Phase = "lobby"
    PhaseRoleAssign  Phase = "role_assign"
    PhaseNight       Phase = "night"
    PhaseNightResolve Phase = "night_resolve"
    PhaseDayAnnounce Phase = "day_announce"
    PhaseDiscussion  Phase = "discussion"
    PhaseVoting      Phase = "voting"
    PhaseLynchResolve Phase = "lynch_resolve"
    PhaseGameOver    Phase = "game_over"
)

type Player struct {
    ID       PlayerID
    Username string
    Role     Role
    Alive    bool
    // JoinedAt used to enforce lobby ordering / late-join rules
    JoinedAt time.Time
    // ProtectedTonight is reset each night
    ProtectedTonight bool
    // Vigilante-specific: has used their one-time kill
    UsedAbility bool
}

type NightAction struct {
    ActorID PlayerID
    Kind    string // "mafia_kill", "detective_check", "doctor_protect", "vigilante_kill"
    TargetID PlayerID
    SubmittedAt time.Time
}

type Vote struct {
    VoterID  PlayerID
    TargetID PlayerID // 0 or sentinel value = "no lynch"
    Timestamp time.Time
}

type GameState struct {
    ID           GameID
    ChatID       int64
    HostID       PlayerID
    Phase        Phase
    DayNumber    int
    Players      map[PlayerID]*Player
    NightActions map[PlayerID]NightAction // keyed by actor, cleared each night
    Votes        map[PlayerID]Vote        // keyed by voter, cleared each day
    Config       GameConfig
    CreatedAt    time.Time
    PhaseDeadline time.Time // used for timer-driven transitions
    Log          []GameEvent // append-only audit log for debugging & replay
}

type GameConfig struct {
    MinPlayers           int
    MaxPlayers           int
    LobbyTimeoutSec      int
    RoleAssignTimeoutSec int // backstop if the role-delivery ack never arrives
    NightTimeoutSec      int
    DiscussionTimeoutSec int
    NominationTimeoutSec int // time to second a nomination before the day ends
    VotingTimeoutSec     int
    LastWordsSec         int

    // Split per §9 below: reveal on lynch, hide night kills.
    RevealOnLynch     bool
    RevealOnNightKill bool

    AllowNoLynch          bool
    LynchRequiresMajority bool // more than half the eligible voters must agree
    MafiaRatioDivisor     int              // default 4 — used by ComputeMafiaCount (n / divisor)
    OptionalRoles         []RoleDefinition // the tunable registry from §5.2.2 (weights, thresholds)
    SpecialRoleDivisor    int              // default 3 — used by SpecialRoleBudget (n / divisor)
    DoctorSelfProtect     bool
    FirstNightKill        bool
    NominationSystem      bool
    AllowLastWords        bool
    // SimultaneousNightActions treats the night as one instant, so a player
    // killed during it still completes their own action.
    SimultaneousNightActions bool
}
```

Every non-terminal phase must have a non-zero timeout; `ValidateConfig` rejects a
config that would let the game park in a phase with nothing scheduled to move it
along.

```go

type GameEvent struct {
    Timestamp time.Time
    Type      string // "player_joined", "night_action", "vote_cast", "phase_change", ...
    Payload   map[string]interface{}
}
```

### 4.1 Storage & Concurrency

- **In-memory registry**: `map[GameID]*GameState` guarded by a `sync.RWMutex`, or better, one goroutine per active game reading from a buffered channel of events (actor-model style) — this avoids lock contention and makes race conditions on simultaneous night-action submissions structurally impossible.
- **Persistence**: snapshot `GameState` to Redis (or Postgres JSONB column) after every phase transition and every action, so a bot crash/restart can resume mid-game. Use the `Log []GameEvent` as the source of truth; `GameState` itself can be a derived/rebuilt projection if you want event-sourcing rigor (recommended given your ledger-system background — this is structurally identical to replaying transactions to rebuild an account balance).
- **Per-game goroutine pattern** (recommended for Go):

```go
type GameActor struct {
    state   *GameState
    inbox   chan Event
    outbox  chan OutgoingMessage // consumed by the Telegram sender
}

func (a *GameActor) Run(ctx context.Context) {
    for {
        select {
        case ev := <-a.inbox:
            a.state = reduce(a.state, ev)
            persist(a.state)
            a.emitSideEffects(ev)
        case <-time.After(time.Until(a.state.PhaseDeadline)):
            a.inbox <- TimeoutEvent{Phase: a.state.Phase}
        case <-ctx.Done():
            return
        }
    }
}
```

This gives you: no shared mutable state across goroutines, natural handling of phase timeouts, and a clean audit trail.

---

## 5. Role Assignment Details

### 5.1 Randomization

- Use `crypto/rand`, not `math/rand`, for role shuffling — players will absolutely try to detect/exploit predictable seeding if this becomes popular.
- Fisher-Yates shuffle the player list, then assign roles by walking a pre-built role slice (e.g., `[mafia, mafia, detective, doctor, villager, villager, villager]`) in order.

### 5.2 Role Distribution — Dynamic & Random Generation

A hardcoded table (as originally sketched) has two real problems: it silently breaks for any player count you forgot to add a row for, and it produces the *exact same* role composition every time a given N plays — no variety, and "random" only in who gets which role, never in what roles exist. Replace it with a **generator**: a formula for the mandatory counts, plus weighted random sampling for which optional roles appear, re-validated for balance before use.

#### 5.2.1 Step 1 — Mafia count (deterministic formula, not random)

Mafia count *should* stay formula-driven rather than randomized — randomizing it too adds volatility without adding fun, and makes balance testing much harder. Scale it to any N with hard bounds:

```go
func ComputeMafiaCount(n int) int {
    count := n / 4              // floor division — roughly 25%
    if count < 1 {
        count = 1               // every game needs at least one mafia
    }
    maxAllowed := (n - 1) / 2   // mafia must never reach half the table
    if count > maxAllowed {
        count = maxAllowed
    }
    return count
}
```

This alone means you never need a table row for N=13, N=17, N=40 — it just works.

#### 5.2.2 Step 2 — Registry of optional roles

Define eligibility thresholds and a sampling weight per optional role, instead of baking "Detective unlocks at 6" into a table cell:

```go
type RoleDefinition struct {
    Role       Role
    Team       Team
    MinPlayers int     // game must have at least this many players for the role to be eligible
    Weight     float64 // relative chance of being picked, once eligible, given available slot budget
    ReplacesMafiaSlot bool // true for Godfather: doesn't grow the mafia team, just re-labels one
}

var OptionalRoles = []RoleDefinition{
    {RoleDetective, TeamTown,    6,  1.0, false},
    {RoleDoctor,    TeamTown,    7,  0.9, false},
    {RoleVigilante, TeamTown,    10, 0.4, false},
    {RoleJester,    TeamNeutral, 8,  0.3, false},
    {RoleGodfather, TeamMafia,   9,  0.5, true},
}
```

Weight is relative, not a probability that sums to 1 — higher weight just means more likely to be picked when slots are being filled. Detective at weight 1.0 vs. Jester at 0.3 means Detective is roughly 3x as likely to claim a slot when both are eligible and competing for the same budget.

#### 5.2.3 Step 3 — Special-role slot budget

How many optional roles get included at all should also scale with N rather than being fixed:

```go
func SpecialRoleBudget(n int) int {
    budget := n / 3          // roughly one special town/neutral role per 3 players
    if budget > len(OptionalRoles) {
        budget = len(OptionalRoles)  // can't exceed the registry size
    }
    return budget
}
```

#### 5.2.4 Step 4 — Weighted random sampling without replacement

Filter `OptionalRoles` to those with `MinPlayers <= N`, then draw `SpecialRoleBudget(n)` of them using cumulative-weight sampling, powered by `crypto/rand` (never `math/rand` — see §5.1):

```go
func SampleOptionalRoles(eligible []RoleDefinition, budget int, rng io.Reader) []RoleDefinition {
    pool := append([]RoleDefinition{}, eligible...)
    var chosen []RoleDefinition
    for i := 0; i < budget && len(pool) > 0; i++ {
        totalWeight := 0.0
        for _, r := range pool {
            totalWeight += r.Weight
        }
        pick := weightedRandomIndex(pool, totalWeight, rng) // cumulative-sum draw
        chosen = append(chosen, pool[pick])
        pool = append(pool[:pick], pool[pick+1:]...) // remove, no repeats
    }
    return chosen
}
```

#### 5.2.5 Step 5 — Assemble & validate

```go
func GenerateRoleSet(n int, rng io.Reader) ([]Role, error) {
    mafiaCount := ComputeMafiaCount(n)
    eligible := filterEligible(OptionalRoles, n)
    budget := SpecialRoleBudget(n)
    chosen := SampleOptionalRoles(eligible, budget, rng)

    roles := []Role{}
    godfatherChosen := false
    for _, r := range chosen {
        if r.ReplacesMafiaSlot {
            godfatherChosen = true
            continue // handled below — doesn't add a slot
        }
        roles = append(roles, r.Role)
    }

    mafiaRoles := make([]Role, mafiaCount)
    for i := range mafiaRoles {
        mafiaRoles[i] = RoleMafia
    }
    if godfatherChosen && mafiaCount >= 1 {
        mafiaRoles[0] = RoleGodfather // relabel one mafia slot, count unchanged
    }
    roles = append(roles, mafiaRoles...)

    villagerCount := n - len(roles)
    if villagerCount < 0 {
        return nil, fmt.Errorf("role budget exceeded player count: n=%d roles=%d", n, len(roles))
    }
    for i := 0; i < villagerCount; i++ {
        roles = append(roles, RoleVillager)
    }

    if err := ValidateBalance(roles, n); err != nil {
        return nil, err // caller retries generation (bounded) or falls back to a minimal-safe set
    }
    return roles, nil
}
```

`ValidateBalance` enforces the invariants from §2.2 / §8.8 before anything is dealt:
- `mafiaCount < ceil(n/2)` (mafia can't already start at parity/majority)
- at least one villager remains (special-role budget can't consume the entire lobby)
- if `RoleJester` was chosen, town's effective kill-capacity (mafia + any town kill roles) can still reach a win — cheap sanity check, not exhaustive game-theory proof
- if generation fails validation, retry with a fresh sample (bounded to, say, 5 attempts) before falling back to "mafia + villagers only, no optional roles" as a guaranteed-safe minimum

#### 5.2.6 Why this is better than the table

- **Scales to any N** without needing a maintainer to add rows.
- **Actually random**, not just in who-gets-what but in *which* optional roles show up — two games with the same 8 players can play out with Doctor+Jester one time and Vigilante+Detective the next.
- **Tunable without redeploying logic** — `Weight`, `MinPlayers`, and the budget formula are the only knobs a host/config needs to touch to reshape the game's feel; no table rows to keep in sync.
- **Testable in isolation** — `ComputeMafiaCount`, `SampleOptionalRoles`, and `ValidateBalance` are pure functions; table-driven tests can assert distribution shape across a wide sweep of N (e.g., N=5..40) instead of eyeballing a handful of hardcoded rows.
- **RNG is injectable** (`rng io.Reader` parameter) — production wires `crypto/rand.Reader`, tests wire a deterministic fake source so distribution logic is asserted against known sequences rather than being flaky.

#### 5.2.7 Audit trail

Log the generated role set (not who received which role — that stays private) into `GameEvent` at role-assignment time: `{"type":"roles_generated","mafia_count":2,"optional_roles":["detective","jester"],"n_players":9}`. This lets you debug "why did this game have a Jester but the last one didn't" without needing to reverse-engineer the RNG draw.

Minimum viable game: **5 players**. Below that, `ComputeMafiaCount` still returns 1, but the special-role budget (`n/3`) rounds to 1 or less, so games this small will rarely field a Detective — expected and fine; DM-based night actions feel thin below 5 players regardless of role variety.

### 5.3 The Full Allocation Algorithm (End-to-End)

§5.2 decides *which* roles exist in a game. This section is the single entry point that combines that with *who* gets them — this is the function the engine actually calls at `ROLE_ASSIGNMENT`.

#### 5.3.1 Algorithm statement

```
AllocateRoles(players, config, rng):
  1. n ← len(players)
  2. if n < config.MinPlayers: return error
  3. roleSet ← GenerateRoleSet(n, config, rng)      // §5.2, retried up to 5x on validation failure
  4. if all attempts failed: roleSet ← MinimalSafeRoleSet(n, config)   // mafia + villagers, always valid
  5. assert len(roleSet) == n                        // defensive; a mismatch here is a bug, not a game-state problem
  6. shuffled ← FisherYatesShuffle(players, rng)      // uniform random permutation of player order
  7. assignment ← zip(shuffled, roleSet)              // position i gets roleSet[i]
  8. return assignment
```

Only **one** shuffle is needed — shuffling `players` and pairing them in order against `roleSet` already produces a uniform random bijection between players and roles. Shuffling `roleSet` as well would be redundant (not incorrect, just wasted entropy and an extra thing to reason about when auditing fairness).

#### 5.3.2 Go implementation

```go
func AllocateRoles(players []PlayerID, cfg GameConfig, rng io.Reader) (map[PlayerID]Role, error) {
    n := len(players)
    if n < cfg.MinPlayers {
        return nil, fmt.Errorf("not enough players: have %d, need %d", n, cfg.MinPlayers)
    }

    var roleSet []Role
    var err error
    const maxAttempts = 5
    for attempt := 0; attempt < maxAttempts; attempt++ {
        roleSet, err = GenerateRoleSet(n, cfg, rng) // §5.2.5, threaded with cfg for OptionalRoles/divisors
        if err == nil {
            break
        }
    }
    if err != nil {
        roleSet = MinimalSafeRoleSet(n, cfg) // mafia (via ComputeMafiaCount) + villagers only — trivially valid
    }

    if len(roleSet) != n {
        // Should be unreachable if GenerateRoleSet/MinimalSafeRoleSet are correct — fail loudly, don't
        // silently truncate or pad, since either would corrupt role balance invisibly.
        return nil, fmt.Errorf("internal error: role set size %d != player count %d", len(roleSet), n)
    }

    shuffled := FisherYatesShuffle(players, rng)

    assignment := make(map[PlayerID]Role, n)
    for i, pid := range shuffled {
        assignment[pid] = roleSet[i]
    }
    return assignment, nil
}

func FisherYatesShuffle(items []PlayerID, rng io.Reader) []PlayerID {
    shuffled := append([]PlayerID{}, items...) // never mutate caller's slice
    for i := len(shuffled) - 1; i > 0; i-- {
        jBig, err := rand.Int(rng, big.NewInt(int64(i+1))) // crypto/rand — see §5.1
        if err != nil {
            panic(err) // CSPRNG failure is a fatal environment problem; never silently fall back to math/rand
        }
        j := int(jBig.Int64())
        shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
    }
    return shuffled
}

// weightedRandomIndex draws an index from pool proportional to each entry's Weight,
// using crypto/rand for the draw (see §5.2.4).
func weightedRandomIndex(pool []RoleDefinition, totalWeight float64, rng io.Reader) int {
    const precision = 1_000_000
    scaledTotal := int64(totalWeight * precision)
    if scaledTotal <= 0 {
        return 0
    }
    nBig, err := rand.Int(rng, big.NewInt(scaledTotal))
    if err != nil {
        panic(err)
    }
    target := nBig.Int64()
    var cumulative int64
    for i, r := range pool {
        cumulative += int64(r.Weight * precision)
        if target < cumulative {
            return i
        }
    }
    return len(pool) - 1 // floating-point rounding guard — last entry catches any residual
}
```

#### 5.3.3 Correctness properties (what "good" means here, made explicit)

1. **Uniformity** — every player is equally likely to receive any given role from the generated set; no positional bias from join order, user ID, or username. Guaranteed by Fisher-Yates being a provably uniform permutation generator, *provided* the RNG driving it is uniform (crypto/rand is).
2. **Independence from identity** — the algorithm never branches on `PlayerID`, `Username`, or `JoinedAt` when deciding roles. (Contrast with a naive "first N joiners are mafia" bug — trivially exploitable and must not exist anywhere in this path.)
3. **Reproducibility only via injected RNG** — production wires `crypto/rand.Reader` (non-reproducible, as required for fairness); tests wire a seeded deterministic `io.Reader` fake so the *shape* of the algorithm (shuffle correctness, weighted sampling correctness) can be asserted against known sequences without weakening production randomness.
4. **Fail-loud on RNG error** — `crypto/rand` reads from the OS entropy source; a read failure is an environment problem (extremely rare, but has happened on misconfigured containers), not something to paper over with a weaker fallback. `panic` here is correct: silently degrading to `math/rand` for role assignment would be a fairness bug, not a robustness win.
5. **Total ordering of validation before assignment** — `GenerateRoleSet` must fully pass `ValidateBalance` (§5.2.5) *before* `AllocateRoles` ever shuffles players. Never assign roles first and validate after — that risks a DM already going out with a role that then has to be silently revoked.

#### 5.3.4 Complexity

- `GenerateRoleSet`: dominated by weighted sampling over the optional-role registry, which is small and fixed (≈5–10 entries) — effectively O(1) relative to player count, technically O(k²) in registry size k for the naive re-sum-per-draw approach shown, which is fine since k is tiny and bounded by the registry, not by n.
- `FisherYatesShuffle`: O(n).
- Overall: **O(n)** for any realistic lobby size — role allocation will never be a performance concern, even at hundreds of players.

#### 5.3.5 Testing strategy

- **Property-based sweep**: for n across a wide range (e.g., 5–50), assert `len(roleSet) == n`, `mafiaCount < ceil(n/2)`, `villagerCount >= 0` on every generated set — catches off-by-one and boundary bugs the static table could never have caught for untested row values.
- **Statistical fairness test**: run `AllocateRoles` thousands of times for a fixed player list and fixed role set composition; chi-squared goodness-of-fit test that each player received each role with statistically uniform frequency. This is the concrete way to *prove* "random," not just assert it in a comment.
- **Deterministic regression test**: fixed seeded fake RNG in → fixed output out, so a future refactor that accidentally changes draw order gets caught by a snapshot diff instead of a flaky statistical test.
- **Fallback-path test**: force `GenerateRoleSet` to fail validation on every attempt (e.g., inject a config that's impossible to satisfy) and assert `AllocateRoles` correctly falls through to `MinimalSafeRoleSet` rather than propagating the error to the caller.

#### 5.3.6 Concurrency note

`AllocateRoles` is a pure function with no shared mutable state of its own — safe to call concurrently from multiple per-game actor goroutines (§4.1) without any locking. The only shared resource is the `rng io.Reader` itself; `crypto/rand.Reader` is documented as safe for concurrent use, so no additional synchronization is needed there either.

### 5.3 DM Delivery

- Bot must have a prior `/start` from every player in DM before the game begins — Telegram bots **cannot** message a user who hasn't initiated a DM first. This is a hard API constraint, not a design choice.
- On `/join`, if the bot has no DM channel open with that user, reply in-group: *"@user, please DM me and press Start before joining so I can send you your role."* Block them from being added to the player list until confirmed.

### 5.4 Role Reveal

- On elimination, whether to reveal the dead player's role is controlled by two independent flags, `RevealOnLynch` and `RevealOnNightKill`. Revealing adds information for the town but reduces mystery — most competitive groups play with reveal ON for lynches and OFF for night kills (the classic "you wake up and X is dead" ambiguity), which is the default. Each player carries a `RoleRevealed` flag so later summaries echo what was actually made public rather than re-deriving the rule.

---

## 6. Telegram Bot Command Surface

| Command | Context | Description |
|---|---|---|
| `/startgame` | Group | Opens a lobby, sets host = caller |
| `/join` | Group | Adds caller to lobby (requires prior DM `/start`) |
| `/leave` | Group | Removes caller from lobby (lobby phase only) |
| `/begin` | Group, host only | Force-starts if `MinPlayers` met |
| `/endgame` | Group, host or admin only | Force-ends, no winner declared |
| `/status` | Group | Shows current phase, alive/dead count (no roles) |
| `/accuse`, `/defend`, `/whisper` | Group, alive | Interactive discussion (§11.3) |
| `/nominate`, `/second` | Group, alive | Trial mode, when `NominationSystem` is on |
| `/host`, `/kick` | Group, host | Transfer host, remove an AFK player |
| `/reveal` | Group, Mayor | Go public in exchange for vote weight |
| `/graveyard` | Group | The dead in the order they died, with public roles only |
| `/roles` | Group or DM | The full role reference, rendered from the catalogue |
| `/settings` | Group, host or admin | Opens the inline settings panel (§11.2) |
| `/stats [@player]` | Anywhere | Lifetime record for one player |
| `/leaderboard [global]` | Group or DM | Best players in this group, or overall |
| `/achievements` | Anywhere | Unlocked and still-to-earn achievements |
| `/lastgame` | Group | Recap of the group's most recent finished game |
| `/myrole` | DM | Re-sends the caller's role card |
| `/mafia <message>` | DM, mafia, night | Private mafia team channel (§11.3) |
| `/ghost <message>` | DM, dead | Private channel among eliminated players (§11.3) |
| (inline buttons) | DM | Night action target selection |
| (inline buttons) | Group | Voting, mood reactions, graveyard, status, presets, rematch |

All destructive commands (`/endgame`) should require confirmation or be host/admin-gated to prevent griefing. `/settings` is gated to group admins and the current host, so one player cannot rewrite the ruleset on everybody else.

Production receives updates by **webhook** (`POST /telegram/webhook`, Telegram `secret_token`). Local runs without `WEBHOOK_URL` delete the webhook and long-poll. The reducer never sees the difference: both paths call the same `dispatchUpdate`.

Every callback payload carries its `GameID` (or `ChatID` for the settings and
rematch panels) and must stay inside Telegram's 64-byte limit, or the button is
silently dead in the chat. `TestEveryCallbackFitsTelegramsLimit` and
`TestEveryKeyboardPrefixIsRouted` check both properties for every keyboard the
bot can build.

---

## 7. Timers

| Phase | Default duration | On expiry |
|---|---|---|
| Lobby | 300s (configurable, or manual `/begin`) | Auto-begin if `MinPlayers` met, else cancel game |
| Role assignment | 30s per player (parallel, so ~30s total with a grace buffer) | Proceed with whoever confirmed; missing players default to no-action |
| Night | 60–90s | Resolve with whatever actions were submitted; missing mafia vote = random target among submitted mafia votes, or no kill if none submitted (configurable) |
| Discussion | 90–120s | Auto-advance to voting |
| Voting | 45–60s | Tally whatever votes exist; ties resolved per §8.6 |

All timers should post a **60-second warning** and **10-second warning** into the relevant chat (group or DM) to reduce AFK stalls.

---

## 8. Edge Cases (Exhaustive)

### 8.1 Lobby & Setup
- **Player joins twice**: idempotent no-op, don't duplicate in player list.
- **Player leaves after `/begin` fired but before role DM sent**: not allowed — lock the roster the instant `/begin`/auto-start triggers; leaving after that is treated as an in-game disconnect (§8.7), not a lobby leave.
- **Host leaves the lobby**: reassign host to the next-earliest joiner, or the first group admin who is also a player.
- **Not enough players when lobby timer expires**: cancel, post a friendly message, return to `IDLE`.
- **Same player tries to `/startgame` while a game is already active in that chat**: reject with "a game is already in progress; use `/status`."
- **Two different chats' games interfering**: impossible if `GameState` is keyed by `ChatID`/`GameID` and actors are isolated — but explicitly test that a player who is in Game A (chat 1) and also joins Game B (chat 2) doesn't leak state between them. Player identity is global (Telegram user ID); game membership must be scoped per chat.
- **Player is already in another active game in a different chat**: allowed by default (mafia bots commonly support this), but DM action buttons must disambiguate which game a callback belongs to — encode `GameID` in the callback_data payload, never assume "the player's only active game."

### 8.2 DM / Delivery Failures
- **Player blocks the bot mid-game**: DM send fails silently from Telegram's side (no exception in some libraries — check `Forbidden: bot was blocked by the user` error code explicitly). Mark player as "unreachable"; their night action defaults to no-action; if they were Mafia, don't let this stall the whole Mafia team's vote (see §8.4).
- **Bot's DM message fails due to rate limiting**: implement exponential backoff and a per-chat outbound queue; Telegram's flood limits (~30 msgs/sec globally, ~1/sec per chat) matter a lot at scale with many DMs firing near-simultaneously at role-assignment time. Stagger DM sends rather than firing all at once.

### 8.3 Role Assignment Failures
- **A player never presses Start in DM, so role delivery fails at game start** (they passed the `/join` check earlier but revoked/blocked since): remove them from the game, redistribute roles among remaining players, and announce in-group "X could not receive their role and has been removed before the game started." Never let a "ghost" player with no delivered role remain in the alive list.
- **Role redistribution mid-assignment changes counts**: recompute against the role table for the new player count rather than just dropping one slot arbitrarily (dropping one Mafia slot when you meant to drop a Villager slot unbalances the game).
- **The failure does not prove the player is unreachable**: only `Forbidden`-class errors (blocked, deactivated, chat not found) justify removing someone and redealing. An exhausted rate-limit retry or a 5xx says nothing about them, and ejecting on those means a busy minute costs the game one player per redeal until the roster falls below the minimum. Those players keep their seat and are marked unreachable instead, which excludes them from every quorum without disturbing the roster.
- **Outcomes from a superseded deal keep arriving**: a redeal does not cancel the DMs of the attempt it replaced, and two failures in one batch are enough to have three deals in flight at once. Every role DM therefore carries the deal it belongs to, and both the completion and the failure events echo it back. The reducer acts only on the deal that is currently outstanding, so an old batch can neither start the night early nor eject a player who has since been sent a fresh role.
- **The acknowledgement arrives after the phase has moved on**: the 45s backstop can start the night before a failure is reported. Redealing is no longer possible then, but the player still never saw their role, so the failure falls through to the unreachable path rather than being discarded.
- **A restart lands mid-assignment**: which DMs went out is not recoverable, so resume re-sends every role under a new deal. Reporting the phase complete without sending anything would start the night for players who never learned their role, with no redeal left to catch it.

### 8.4 Night Phase
- **Mafia disagree / split vote**: define a resolution rule up front — plurality wins; on a tie, no kill occurs that night (safer default that avoids RNG-based unfairness accusations), or resolve by earliest-submitted vote among the tied targets (configurable).
- **Mafia targets a player who died from another effect that same night** (shouldn't be possible in base ruleset since only one kill source exists at night, but becomes relevant once Vigilante is enabled): resolve all night actions in a fixed deterministic order — Doctor protection applied first (as a status), then kill sources (Mafia, Vigilante) applied against that protection status, then Detective checks (informational, no state mutation) last. Document this order explicitly since it affects outcomes when multiple kill sources target the same player.
- **Doctor protects the Mafia's target**: kill is negated; night announcement says "no one died," never reveals who was protected or by whom.
- **Doctor protects themselves**: if `Doctor self-protect` is disabled in config, reject the action client-side (grey out that button) rather than silently ignoring it.
- **Detective checks a dead player**: reject — target list for all night actions should be pre-filtered to alive players only, refreshed at phase start, not cached from lobby time.
- **Detective checks the Godfather**: returns "Town" (that's the Godfather's entire gimmick) — this is intentional, document it so it isn't mistaken for a bug.
- **Vigilante kills an innocent, then feels remorse mechanics (optional variant)**: out of scope for v1; note as a future extension point.
- **A player is targeted by both Mafia and Vigilante the same night**: still only dies once; don't double-count in death log, but do log both attempts for audit/debugging.
- **Mafia member is the only Mafia alive and is also the one who must submit the kill vote — no "team" to average across**: single-Mafia games just take that one vote directly; the resolution-tie logic degenerates gracefully since there's nothing to tie.
- **All Mafia members are eliminated during the night resolution itself** (e.g., a hypothetical role that kills a random player including mafia): re-run win check immediately after night resolution, before posting the day announcement — don't wait for the day/vote cycle to notice the game already ended.
- **Player submits a night action, then submits a second one before the phase ends** (changed their mind): allow override — last submission before deadline wins. Store by actor ID as a map key (not append-only list) precisely so overwrites are natural.
- **Player tries to submit a night action after the phase has already ended (network lag on their end)**: reject with "the night phase has ended," don't silently accept and mutate a resolved state.
- **All required night roles are dead (e.g., Detective and Doctor both eliminated already) — does the night phase still occur?**: yes, always run the phase (Mafia still needs to kill), just skip prompting roles that have no living representative.

### 8.5 Day Announcement
- **Multiple deaths in one night** (once Vigilante is added, up to 2 deaths possible): announce all deaths together, not as separate messages that could be misread as sequential days.
- **Zero deaths**: must explicitly say "no one died last night" — silence is ambiguous and players will assume a bug.

### 8.6 Voting Phase
- **Tie vote**: configurable resolution — (a) no one is lynched, (b) runoff revote between tied players only, or (c) random among tied. Recommend (a) as default — safest, least controversial, matches most physical-tabletop rulesets.
- **Player votes for themselves**: allowed by most rulesets; don't block it.
- **Player changes their vote mid-phase**: allowed, last vote counts (same override pattern as night actions).
- **Dead player tries to vote**: reject at the button-generation level — dead players shouldn't even see voting buttons in group chat; if using inline keyboards shared publicly, validate on callback regardless (don't trust client-side filtering alone, since callback_data can technically be replayed/spoofed by a determined user — always re-check `Alive` server-side).
- **"No lynch" option**: if `AllowNoLynch` is true, include it as a selectable target; if it wins plurality, no elimination occurs, and this consumes the day without a win-condition check changing.
- **Not enough votes cast before timeout** (e.g., half the group is AFK): tally whatever exists; if literally zero votes cast, treat as "no lynch."
- **A player is voted out but was already dead from an earlier undetected state bug**: defensive check — vote resolution must re-validate the target is alive before applying elimination; this should be structurally impossible if dead players are filtered from vote targets, but assert it anyway (fail loudly to logs, don't silently corrupt state).

### 8.7 Disconnection / Inactivity
- **Player goes AFK for an entire game (never acts, never votes)**: don't auto-kick by default in v1 (adds complexity); instead, surface AFK players via `/status` so the human host can `/kick` them manually. Auto-kick after N consecutive missed phases is a good v2 feature.
- **Player leaves the Telegram group mid-game** (via Telegram's own leave, not a bot command): detect via the `left_chat_member` update; mark as disconnected but keep their role "alive" in game logic unless they were about to be a deciding vote — treat identically to an AFK player (miss their actions, remain targetable) rather than force-ending the game, since forcibly ending on every group-leave is an easy griefing vector (anyone can tank the game by leaving).
- **Bot itself restarts mid-game** (deploy, crash): on boot, reload all `GameState` snapshots from persistence, recompute `PhaseDeadline` (if it already passed during downtime, immediately fire the timeout event rather than waiting), and re-arm timers. Post a short "the game has resumed" message to affected group chats so players aren't confused by the gap.
- **Host is unreachable and a host-only action is needed** (e.g., stuck lobby): allow group admins as a fallback for host-gated commands, or add a `/host` transfer command votable by remaining players.

### 8.8 Endgame & Win Conditions
- **Mafia count equals Town count exactly**: Mafia wins immediately (they can force any vote to a tie or win it outright) — check this at the *end of night resolution*, not just after lynches, since night kills can also produce this state.
- **Jester wins but the game has other neutral/team objectives still unresolved**: Jester's win is independent and doesn't end the game for Town/Mafia — the game continues, and final results list all winners across all resolved conditions.
- **Last two players are one Mafia and one Town, and it's currently Night**: Mafia kills Town, Mafia wins — no need to proceed to a day phase that can't change the outcome; short-circuit here for a snappier ending.
- **All remaining players are removed/disconnected simultaneously** (rare but possible with mass leaves): declare game void, no winner, log the anomaly.
- **A role's ability could theoretically prevent any win condition from ever triggering** (config/balance bug, e.g., Doctor + only 1 Mafia who can never get through): not a runtime edge case but a config-validation one — validate role tables at startup to guarantee mafia can always eventually win via attrition math, reject invalid configs before they reach production.

### 8.9 Anti-Cheat / Integrity
- **Players sharing role info outside the bot** (e.g., screenshotting DM and posting in group): not technically preventable — document as a "social contract" limitation, optionally add a lightweight honor-system reminder message at game start.
- **Player uses a second Telegram account to see "both sides"**: also not preventable by the bot; out of scope.
- **Callback query spoofing / replay**: always validate `(GameID, PlayerID, Phase, ActionKind)` server-side against current state before applying any action from a callback — never trust that a button shown to a client is still valid by the time the callback arrives (phase may have already advanced).
- **Rapid-fire duplicate callback taps** (double-click): make action application idempotent per `(PlayerID, Phase)` — reprocessing the same submitted action should be a safe no-op or a clean override, never a double-count.

---

## 8a. Roster Locking Policy — No Joins After Game Start

This is a small rule with a large edge-case surface, mostly because of *timing*. Treat the roster as **immutable the instant the game leaves `LOBBY`**, and design every related check around that single invariant.

### 8a.1 The core rule

- `/join` is only valid when `Phase == LOBBY`. In every other phase, reject it.
- The rejection is not silent: reply once with something like *"This game already started — you can't join mid-game. I'll let you know when the next one opens."*
- The lock happens at the exact moment `LOBBY → ROLE_ASSIGNMENT` fires (via `/begin`, or the lobby timer auto-starting), not at some later point like "after roles are DMed." If you lock late, you open a window where a join can sneak in after the host thinks the game has started.

### 8a.2 The race condition (most important part)

The dangerous case: a player sends `/join` in the same instant the host sends `/begin` (or the lobby timer fires). Telegram delivers these as two separate updates, and network/webhook jitter means arrival order is not guaranteed to match "real world" order.

**Resolution**: don't try to reconstruct real-world ordering — it's unknowable and not worth chasing. Instead, rely on strict serialization:

- Both events (`JoinEvent`, `BeginEvent`) must funnel through the *same* per-game inbox channel described in §4.1.
- The actor goroutine processes one event at a time, in the order they entered the channel.
- Whichever arrives first at the channel is authoritative — full stop. If `JoinEvent` is processed first, that player is in the roster before `BeginEvent` locks it. If `BeginEvent` is processed first, `JoinEvent` is rejected on the next line.
- This is why the actor/reducer pattern in §3–4 matters beyond code cleanliness: a shared mutex around ad-hoc handler code is much easier to get subtly wrong here (e.g., two goroutines both reading `Phase == LOBBY` before either writes), whereas a single-consumer channel makes the race structurally impossible rather than "unlikely."
- Do **not** use client-side timestamps (message `date` field) to decide ordering — clock skew and Telegram's own delivery latency make this unreliable and, worse, spoofable. Use server-received order only.

### 8a.3 Spam / repeated late-join attempts

- A player who doesn't realize the game started may hammer `/join` repeatedly. Reply once, then apply a short per-user cooldown (e.g., 30s) during which further `/join` calls from that user in that chat are silently dropped rather than re-replied to — otherwise a confused or trolling user can flood the group.
- This cooldown is per `(ChatID, PlayerID)`, not global, so it doesn't affect other users' first rejection message.

### 8a.4 Waitlist (recommended UX addition)

- Maintain a lightweight **chat-level** waitlist (`map[ChatID][]PlayerID`) that is independent of `GameState` — it must survive the current game ending and a new `GameState` being created, so store it outside the per-game struct (e.g., a separate Redis key per chat, not per game).
- Anyone rejected by `/join` during an active game is automatically added to this waitlist (if not already present).
- When the *next* `/startgame` opens a fresh `LOBBY` in that chat, ping waitlisted users once ("the next game just started, `/join` now!") and clear the waitlist. Don't auto-join them — an explicit `/join` still confirms they actually have DM access to the bot (see §5.3), which the waitlist entry alone doesn't guarantee.

### 8a.5 No mid-game roster edits, even by the host

- Deliberately provide **no** host/admin override to force-add a player after start, even for "oops we forgot someone." Reasons:
  - Role-count tables (§5.2) are computed against a fixed player count; inserting a player after roles are dealt has no well-defined role to give them.
  - A late addition would have missed prior night/day information that already shaped other players' knowledge (e.g., a Detective's earlier check), creating an information asymmetry that isn't present for anyone who joined at roll-call.
  - The correct recovery path is `/endgame` (host or admin) followed by a fresh `/startgame` — cheap enough in a chat game that the extra step is worth the correctness guarantee.
- Document this tradeoff explicitly so a future contributor doesn't "helpfully" add a `/forcejoin` command that reopens the race window.

### 8a.6 Leaving vs. joining after start — don't conflate them

- §8.7 already covers what happens when a player **leaves** (disconnects, quits the Telegram group, blocks the bot) after the game starts — that's handled as an in-game disconnect, roster stays locked, remaining players continue.
- This section (§8a) is specifically about the **join** side. Keep the two code paths separate: a rejoin attempt by someone who *was already in the roster* and merely went inactive is not a "late join" — if they come back and re-engage (e.g., unblocks the bot, sends `/myrole`), that should work normally, since they were never removed from `Players`, just marked disconnected/unreachable.
- Concretely: `/join` checks "is this a brand-new player trying to enter the roster," while reconnection is just "an existing roster member interacting again" — different handlers, different validation, don't merge them into one code path even though both involve a player message arriving mid-game.

### 8a.7 Idempotent `/begin` / auto-start

- If two admins both send `/begin` around the same time, or a manual `/begin` races with the lobby auto-start timer, the *first* to reach the actor's inbox performs the `LOBBY → ROLE_ASSIGNMENT` transition; every subsequent `BeginEvent` for a game already past `LOBBY` is a no-op, and the sender gets "the game has already begun" rather than a silent failure or, worse, a double-transition that re-shuffles roles mid-flight.

### 8a.8 Crash/restart interaction with the lock

- On process restart, the recovered `GameState` (from persistence, §4.1) must be checked for its *actual* restored `Phase` before evaluating any queued `/join` — don't let a stale in-memory default (e.g., a fresh struct zero-valuing to something join-permissive) briefly accept joins during the recovery window. Recovery should transition straight from "no in-memory state" to "state with correct locked phase" with no intermediate window where the invariant doesn't hold.

### 8a.9 Spectators are not players

- Non-joined members of the group can still read discussion and see public phase announcements — that's just normal group chat visibility, nothing to build.
- But every vote-callback and night-action handler must validate `PlayerID ∈ GameState.Players` (not just `Alive == true`), so a spectator who somehow triggers a stale inline keyboard (e.g., one shown before they left, or a forwarded message) can't cast a vote or night action. Checking `Alive` alone is insufficient since a non-player's zero-value `Player` struct could otherwise pass an incautious check.

---

## 9. Suggested Repo Structure (Go)

```
mafia-bot/
├── cmd/bot/main.go
├── internal/
│   ├── engine/          # pure state machine, no Telegram deps, fully unit-testable
│   │   ├── state.go         # types, GameConfig, presets, GameState
│   │   ├── roleinfo.go      # the one role catalogue: teams, prompts, targeting
│   │   ├── roles.go         # role generation and allocation
│   │   ├── reduce.go        # the reducer: one event in, state + effects out
│   │   ├── night.go         # night resolution order and its steps
│   │   ├── social.go        # reveal, reactions, mafia chat, ghost chat
│   │   ├── settings.go      # the settings registry the panel is built from
│   │   ├── summary.go       # GameSummary, per-player results, awards
│   │   ├── format.go        # vote board, graveyard, recap, progress bars
│   │   └── markdown.go      # escaping for everything user-supplied
│   ├── stats/           # durable player records, derived purely from GameSummary
│   │   ├── stats.go         # PlayerStats, streaks, leaderboard scoring
│   │   ├── achievements.go  # the achievement catalogue
│   │   └── format.go        # stat cards, leaderboards, recaps, GameRecord
│   ├── telegram/        # transport layer: handlers, keyboards, message formatting
│   │   ├── handlers.go      # command routing and effect dispatch
│   │   ├── webhook.go       # HTTPS webhook (setWebhook + secret_token) or polling
│   │   ├── stats_cmd.go     # stats commands and the settings panel
│   │   ├── keyboards.go     # every inline keyboard
│   │   ├── boards.go        # message IDs of the messages edited in place
│   │   ├── results.go       # folds a finished game into durable records
│   │   ├── roledelivery.go  # role DM delivery and confirmation
│   │   └── sender.go        # rate-limited outbound queue, edits, retries
│   ├── store/           # persistence (MongoDB Atlas, or in-memory)
│   │   ├── store.go         # Store interface, ChatSettings, MemoryStore
│   │   └── mongo.go         # games, stats, history, settings collections
│   └── actor/           # per-game goroutine supervisor
│       └── actor.go
├── go.mod
└── README.md
```

Keeping `engine/` free of any Telegram import is the single highest-leverage design decision here — it means the entire rules engine (including every edge case above) can be covered by table-driven Go tests without spinning up a bot or mocking HTTP calls.

`stats/` follows the same rule one level out: it depends on `engine` but not on
Telegram or on any database. Everything it produces is a pure transformation of
an `engine.GameSummary`, so streaks, achievements and leaderboard ranking can be
tested without a bot or a Mongo instance.

---

## 10. Suggested Build Order

1. Engine package + full unit test suite covering §8 edge cases (build this **before** touching the Telegram API at all).
2. Telegram transport: lobby + role DM delivery only, manual `/begin`, no timers yet.
3. Night phase with DM inline keyboards, deterministic resolution.
4. Day/voting phase with group inline keyboards.
5. Timers + auto-advance.
6. Persistence + crash recovery.
7. Polish: `/status`, host transfer, AFK handling, role reveal config.

This gets you a fully playable (if manually-paced) game after step 4, with steps 5–7 being reliability/UX hardening rather than core-loop work.

---

## 11. Progression, Live UI, and Group Configuration

Everything in this section sits *outside* the core loop. None of it can change
the outcome of a game, which is deliberate: a failure in the stats pipeline or a
Telegram edit that will not apply must never stall the group's next round.

### 11.1 Player records, achievements, and leaderboards

The engine's only output is `engine.GameSummary`, built once at `endGame`. It
carries the roster with each player's role, whether they survived, whether they
won, how they died, their per-game counters, the awards, and the timeline. The
`stats` package consumes it and nothing else.

```
endGame → GameSummary → GameOverEffect → recordResults()
                                          ├─ SaveGameRecord  (chat history)
                                          └─ per player: PlayerStats.Apply()
                                                          ├─ totals, streaks, per-role record
                                                          ├─ awards collected
                                                          └─ newly unlocked achievements → DM
```

Design decisions worth stating explicitly:

- **An aborted game counts as neither a win nor a loss, and leaves the streak
  untouched.** A game cancelled by the host, or voided because everyone
  disconnected, produced no real result. Without this rule a player could
  protect a streak by abandoning a losing position.
- **A game still counts as *played* when aborted.** It happened; it just did not
  produce a result.
- **A player who never received a role is skipped entirely**, so a lobby that
  collapsed before the deal does not give anyone a phantom game.
- **Win rate is measured over decided games only** (`Wins / (Wins + Losses)`),
  not over games played.
- **Leaderboard score discounts unproven records.** Wins drive it, win rate
  breaks ties, and a five-game confidence factor keeps a single lucky game from
  topping the board. Ties resolve on wins and then player ID, so the order is
  deterministic.
- **Achievements are evaluated after the game has been folded in**, so a
  condition can read either the lifetime totals or the single game that just
  finished. They unlock once and are never re-awarded. Secret achievements stay
  hidden from `/achievements` until earned.
- **A per-chat leaderboard only lists players who have finished a game in that
  chat**, tracked from the archived game records rather than from live state.

Every failure in this pipeline is logged and skipped rather than propagated:
`TestRecordResultsSurvivesAFailingStore` runs the whole path against a store
that rejects every write.

### 11.2 Presets and the inline settings panel

`internal/engine/settings.go` holds a registry of every knob a group can change.
The panel renders from the registry, stored overrides are applied through it, and
nothing else needs to know the field names. Adding a setting is one entry.

A group's configuration is stored as **a preset name plus the individual
overrides layered on top of it**, not as a flattened config. That way a later
change to a preset's defaults still reaches groups that had only tweaked one
unrelated thing.

| Preset | Intent |
|---|---|
| `classic` | The balanced default ruleset |
| `speed` | Half-length phases for a quick game |
| `chaos` | Twice the special roles, lovers, serial killer, reveals on |
| `ranked` | Strict: no last words, no skipping, nothing revealed |

Three layers of validation keep a stored setting from ever breaking a game:

1. `ApplySetting` ignores an unknown key, and a value outside a choice
   setting's list.
2. The panel refuses to *save* a combination that fails `ValidateConfig`.
3. `ChatSettings.Config()` falls back to the bare preset if the stored
   overrides no longer validate — which is what happens when a setting is
   removed in a later build.

`TestSettingsApplyOnTopOfEveryPreset` exercises every setting against every
preset, so no reachable combination is unplayable.

### 11.3 Live UI and private channels

Three of Telegram's constraints shape this layer:

- **Editing beats posting.** During a vote the bot keeps one message updated in
  place rather than announcing each ballot, which is both calmer to read and far
  cheaper against the per-chat rate limit. `boards.go` tracks the message IDs
  being rewritten. This is purely presentational: if an ID is lost to a restart
  the caller posts a fresh message, so nothing breaks.
- **An unchanged edit is an error.** Refreshing a board that has not changed is
  a normal outcome of a live UI, and Telegram rejects it with `message is not
  modified`. `isUnchangedEdit` recognises it so it does not fill the log or
  trigger pointless retries.
- **Callback data is capped at 64 bytes** and carries the game or chat ID.

The countdown during a vote refreshes the existing board instead of posting a
warning. The night warning does post, and names who still owes an action — the
single most useful nudge available, and one that leaks nothing, since the roster
is already public.

Two private channels are relayed entirely through DMs and never touch the group:

- **Mafia night chat** (`/mafia`) is restricted to the mafia and to the night,
  so the team cannot coordinate in real time while the town is reading the room.
- **Ghost chat** (`/ghost`) is restricted to eliminated players, so the dead can
  speculate freely without spoiling anything for the living.

Both escape every relayed message, because a message body is user-supplied text
travelling into a Markdown-formatted DM.

### 11.4 Night resolution order

Adding roles that interact turned the night into an ordered pipeline. Each step
depends only on the ones before it, which is what keeps the whole night
deterministic:

1. **Roleblocks** — decide whose actions happen at all. A roleblock cannot
   itself be blocked, otherwise two escorts pointing at each other would need an
   arbitrary tiebreak.
2. **Visits** — record who went where, for the Lookout. Only the mafia member
   who carries out the kill is seen, so one stakeout cannot expose the whole
   team.
3. **Framing** — alter what an investigation will report, for this night only.
4. **Protection** — doctors mark their patient, bodyguards take up position.
5. **Kills** — mafia (by plurality; a tie kills nobody), then serial killer,
   then vigilante. Each attempt runs the protection chain: a doctor stops it
   outright, otherwise a bodyguard trades their life and takes the attacker with
   them. One bodyguard absorbs one attack.
6. **Information** — detective and lookout results, delivered only to actors who
   are still alive to act on them.
7. **Grief** — lovers follow each other into the grave, through `killPlayer`.

`SimultaneousNightActions` decides whether a player killed earlier in the same
night still completes their own action. Both settings are covered by tests, since
the two produce genuinely different outcomes.