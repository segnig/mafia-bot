# Mafia Bot — Complete Player Guide

> **In Telegram:** type `/guide` for a link to this document, or `/help` for in-chat shortcuts.

## Table of Contents

1. [What is Mafia?](#what-is-mafia)
2. [Getting Started](#getting-started)
3. [Lobby & Hosting](#lobby--hosting)
4. [Game Flow & Phases](#game-flow--phases)
5. [Night Resolution](#night-resolution)
6. [Day, Discussion & Trial Mode](#day-discussion--trial-mode)
7. [Voting & Lynching](#voting--lynching)
8. [All Roles (Detailed)](#all-roles-detailed)
9. [Role Pool & Balance](#role-pool--balance)
10. [Lovers Modifier](#lovers-modifier)
11. [Win Conditions](#win-conditions)
12. [Settings & Presets](#settings--presets)
13. [All Commands (Detailed)](#all-commands-detailed)
14. [Help System](#help-system)
15. [Stats, Leaderboard & Achievements](#stats-leaderboard--achievements)
16. [End-of-Game Awards](#end-of-game-awards)
17. [After the Game](#after-the-game)
18. [Silent & Unreachable Players](#silent--unreachable-players)
19. [Bot Restarts & Recovery](#bot-restarts--recovery)
20. [Strategy Tips](#strategy-tips)
21. [Etiquette](#etiquette)
22. [FAQ](#faq)

---

## What is Mafia?

Mafia (also called Werewolf) is a social deduction game for 5–20 players in a Telegram group.

**Town** must find and eliminate every hidden threat. **Mafia** infiltrate the town and win by reaching **equal numbers** with everyone else. Depending on settings, a **Serial Killer** may hunt alone, and **neutral** roles may win on their own terms.

There is no hidden moderator — the bot runs every phase, timer, vote, and night action.

---

## Getting Started

### First-time setup (every player)

1. **DM the bot** and send `/start`. Telegram requires this before the bot can message you.
2. **Add the bot** to your group chat (needs permission to read messages and send replies).
3. In the group, someone runs `/startgame`.

### Starting a game

1. Host runs `/startgame` → a **lobby card** appears with player list, fill bar, and mode.
2. Players `/join` or tap **Join Lobby**.
3. **Host** configures rules (`/settings`, `/set`) — see [Settings & Presets](#settings--presets).
4. Host runs `/begin` when at least **5 players** have joined.

### Requirements before `/begin`

- Minimum **5** players, maximum **20**.
- Every player must have **DM'd `/start`** before roles are dealt.
- If someone **blocked the bot** before Night 1, they are removed and roles are **redealt** without them.
- If a role DM **fails transiently**, the player stays as 📵 **silent** (cannot act, does not block the game).

---

## Lobby & Hosting

### Lobby card

The lobby shows:
- 👑 **Host** name
- 👥 **Player list** (join order)
- Fill bar (`players / max`)
- ⚙️ **Mode** (preset name and description)
- **Join Lobby** and **Configure** buttons

### Lobby timer

Default: **5 minutes** (300 seconds). Configurable with `/set lobby <seconds>` (60–1800).

- If the timer expires **without enough players**, the lobby is **cancelled**.
- If enough players joined, the game can **auto-start** when the timer fires.

### Host powers (lobby only)

| Action | How |
|--------|-----|
| Configure rules | `/settings` or tap **⚙️ Configure** |
| Custom values | `/set night 75`, `/set lovers on`, etc. |
| Start game | `/begin` |
| Transfer host | `/host @player` |

**Only the host** can change settings. Group admins cannot configure rules (but can `/kick`, `/endgame`).

### Waitlist

If you `/join` while a game is already running, you are added to a **waitlist**. When the next `/startgame` opens a lobby, waitlisted players are pinged — they must still `/join` explicitly.

### Schedule a game

Plan ahead so the group knows when the next lobby opens.

| | |
|---|---|
| **Command** | `/schedule` |
| **Where** | Group only |
| **Who** | Anyone (scheduler becomes host) |
| **When** | No game currently running |

**Set a time**
```
/schedule in 2h
/schedule in 45m
/schedule at 20:00          ← 24-hour clock, UTC
```

**Check or cancel**
```
/schedule                   ← show pending schedule
/schedule cancel            ← host or admin only
```

**What happens at the scheduled time**
- Bot posts **"Scheduled game time!"** in the group
- A lobby opens automatically — you are **host**
- Waitlisted players get pinged (same as `/startgame`)
- Configure with `/settings`, then `/begin` when ready

**Rules**
- One schedule per group (scheduling again **replaces** the old one)
- Minimum **5 minutes** ahead, maximum **7 days** ahead
- If a game is already running when the time hits, the schedule is skipped
- Manual `/startgame` clears any pending schedule
- Schedules survive bot restarts (stored in MongoDB)

**Example**
```
Alice: /schedule in 2h
Bot: 🗓️ Game scheduled for Wed Sep 2, 20:04 UTC (in 2 hours).
     You'll host when the lobby opens…

(two hours later)
Bot: 🗓️ Scheduled game time!
     @alice is hosting. Tap Join Lobby or /join.
```

### Rematch

After a game ends, tap **🔄 Rematch** on the recap card to open a new lobby instantly. The tapper becomes host (must have DM'd `/start`).

---

## Game Flow & Phases

```
LOBBY → ROLE DMs → NIGHT → DAY ANNOUNCE → DISCUSSION → (NOMINATION) → VOTING → LAST WORDS → NIGHT → … → GAME OVER
```

| Phase | What happens |
|-------|--------------|
| **Lobby** | Players join; host configures and runs `/begin` |
| **Role assign** | Bot DMs every player their role; Night 1 waits until all DMs succeed |
| **Night** | Mafia and power roles act via **inline buttons in DM** |
| **Night resolve** | Bot applies all actions in order (see below) |
| **Day announce** | Who died overnight (roles revealed if setting enabled) |
| **Discussion** | Debate, accuse, defend, whisper, mood reactions |
| **Nomination** | *(Trial mode only)* `/nominate` + `/second` window |
| **Voting** | Live vote board; tap to vote or change vote |
| **Last words** | *(Optional)* Lynched player speaks briefly |
| **Game over** | Recap, awards, rematch button |

### Default timers (Classic preset)

| Phase | Default | Configurable key | Range |
|-------|---------|------------------|-------|
| Lobby | 300s (5 min) | `lobby` | 60–1800 |
| Role assign | 45s backstop | — | — |
| Night | 90s | `night` | 20–300 |
| Discussion | 120s | `discussion` | 30–600 |
| Nomination | 45s | — | — |
| Voting | 60s | `voting` | 15–180 |
| Last words | 15s | — | — |

⚡ **Speed preset** halves most timers. See [Presets](#presets).

---

## Night Resolution

When the night timer ends (or all required actions are submitted), the bot resolves everything in this **fixed order**:

1. **Roleblocks** — Escort blocks a target; their action is cancelled
2. **Visits** — Who visited whom is recorded (for Lookout)
3. **Framing** — Framer plants false evidence for tonight's investigations
4. **Protection** — Doctor marks a patient; Bodyguard takes position
5. **Kills** — Mafia kill (requires team agreement), Serial Killer, Vigilante shot
6. **Information** — Detective results and Lookout reports are sent
7. **Grief** — Lovers die together when one falls

### Kill & protection rules

- **Doctor** — Completely stops a kill on their patient that night.
- **Bodyguard** — If the patient would die and wasn't saved by Doctor, the Bodyguard dies instead and **kills the attacker** (one attack absorbed per night).
- **Doctor + Bodyguard** on same target — Doctor save applies first; Bodyguard only steps in if Doctor didn't save.
- **Mafia kill** — All mafia must **agree on the same target** via night buttons. Split votes = **no kill**.
- **Night 1** — If `first_night_kill` is off, mafia get a planning night with no kill (mafia chat still works if enabled).
- **Vigilante** — **One bullet** for the entire game. Last button before timer wins.
- **Escort** — Cannot be roleblocked by another Escort (prevents infinite loops).

### Simultaneous actions

By default, a player killed during the night **still completes their own action** that night (simultaneous resolution). A dead Vigilante's bullet still fires; a dead Detective still gets a result.

### Investigation & disguise

| Role | Investigation reads as |
|------|------------------------|
| Villager, Town powers | Town |
| Mafia | Mafia |
| Godfather | **Town** |
| Serial Killer | **Mafia** |
| Framed player (that night) | **Mafia** |

---

## Day, Discussion & Trial Mode

### Discussion commands (alive & reachable only)

| Command | Effect |
|---------|--------|
| `/accuse @player` | Public accusation; bot tracks tally |
| `/defend [text]` | Formal defense statement — **once per day** |
| `/whisper @player [msg]` | Private DM to one player; **group sees that a whisper happened** (not the text). Or reply to their message + `/whisper text` |
| `/reveal` | **Mayor only** — go public; vote counts as **3** |
| Tap mood bar | One reaction per day (if enabled); shown in day summary |

You can also **reply to a message** instead of `@mention` for `/accuse`, `/nominate`, `/second`, and `/whisper`.

### Trial mode (`nomination` setting)

When enabled, voting requires a trial first:

1. Someone `/nominate @player` during discussion
2. Someone else `/second @player` (the nominated player) within the nomination timer
3. If seconded → **voting** opens for that player only
4. If **nobody seconds** before the timer → day ends with **no lynch**

### Activity tracking

The bot tracks who spoke during discussion. Silent players are named in the day summary. Being quiet is a visible choice.

---

## Voting & Lynching

### Live vote board

One message is **updated in place** showing:
- Vote count per candidate
- Visual bars
- Who voted for whom
- Turnout and countdown

Tap a name to vote; tap another to **change** your vote.

### Lynch threshold

Default: **majority of total voting weight** — more than half.

- With 8 ordinary voters, you need **5** votes to lynch.
- A revealed **Mayor** vote weighs **3**, but also **raises the bar for everyone** — revealing doesn't automatically hand them the day.
- **Silent/unreachable** players are excluded from the eligible voter pool so one dropout can't stall the round.
- If `majority` setting is off, a single vote can lynch when turnout is low.
- **Ties** or falling short → **no lynch** that day.

### Skip today

If `no_lynch` is enabled, the vote board includes **🕊️ Skip Today**.

### Last words

If `last_words` is enabled, the lynched player gets **15 seconds** (configurable via preset) to speak before execution.

### Role reveal on lynch

If `reveal_lynch` is on (default Classic), the lynched player's role is shown to the group.

---

## All Roles (Detailed)

Send `/roles` for a quick list, or `/help detective` (etc.) for in-chat detail.

### 🏘️ Town

#### Villager
- **Team:** Town
- **Night action:** None
- **Ability:** Your vote and your voice. No special power — pure deduction and persuasion.

#### 🔍 Detective
- **Team:** Town | **Min players:** 6
- **Night action:** Investigate one player; learn their **faction** (Town / Mafia / Killer / Neutral team label)
- **Caveat:** Godfather reads as Town; Serial Killer reads as Mafia; Framer can falsify a result for one night

#### 💊 Doctor
- **Team:** Town | **Min players:** 7
- **Night action:** Protect one player from **all kills** that night
- **Setting:** `doctor_self` allows self-protection

#### 🛡️ Bodyguard
- **Team:** Town | **Min players:** 9
- **Night action:** Guard one player. If they are attacked and not Doctor-saved, **you die instead** and **kill the attacker**
- **Limit:** Absorbs one attack per night

#### 💃 Escort
- **Team:** Town | **Min players:** 8
- **Night action:** Roleblock one player — whatever they planned **does not happen**
- **Note:** Escort cannot block another Escort

#### 🔭 Lookout
- **Team:** Town | **Min players:** 8
- **Night action:** Watch one player's house; learn **exactly who visited** them
- **Note:** For mafia kills, only the member who carries out the kill is seen (not the whole team)

#### 🏛️ Mayor
- **Team:** Town | **Min players:** 9
- **Night action:** None
- **Day action:** `/reveal` once — everyone learns you are Mayor; your vote counts as **3** thereafter. You become a prime mafia target.

#### 🔫 Vigilante
- **Team:** Town | **Min players:** 10
- **Night action:** Shoot one player — **once per game**
- **Warning:** Killing town is costly; choose carefully

### 🔪 Mafia

#### Mafia
- **Team:** Mafia
- **Night action:** Team kill — all mafia must pick the **same victim** or nobody dies
- **Team chat:** `/mafia message` in DM at night (if `mafia_chat` enabled)
- **DM at deal:** You learn your mafia teammates

#### 🎩 Godfather
- **Team:** Mafia | **Min players:** 9 | Replaces a mafia slot
- **Night action:** Same as Mafia kill
- **Special:** Investigations always read as **Town**

#### 🖊️ Framer
- **Team:** Mafia | **Min players:** 11 | Replaces a mafia slot
- **Night action:** Frame one player — any investigation of them **that night** reads Mafia
- **Duration:** One night only

### 🩸 Killer

#### Serial Killer
- **Team:** Killer | **Min players:** 11
- **Night action:** Kill one player **every night**, alone
- **Win:** Reach parity with no mafia left
- **Investigations:** Reads as Mafia

### 🃏 Neutral

#### Jester
- **Team:** Neutral | **Min players:** 8
- **Night action:** None
- **Win:** Get yourself **lynched during the day**. Game continues for others after you win.

#### Survivor
- **Team:** Neutral | **Min players:** 10
- **Night action:** None
- **Win:** Be **alive when the game ends**, regardless of which faction wins

---

## Role Pool & Balance

Each game generates roles fresh from the player count:

- **Mafia count** ≈ `players ÷ 4`, minimum 1, never starting at parity
- **Special role budget** ≈ `players ÷ special_roles divisor` (default divisor **3**; Chaos uses **2** = twice as many specials)
- Each optional role has a **minimum player count** before it can appear (see role list above)
- **Godfather** and **Framer** replace mafia slots rather than adding extra enemies

Two games with the same group can have completely different compositions.

---

## Lovers Modifier

When `lovers` is enabled (Chaos preset default):

- Two players are **secretly paired** at the deal
- Each lover is told their partner in DM (not their partner's role)
- **When one dies, the other dies immediately** — "grief", regardless of role or team
- A mafioso paired with a townsperson creates extremely tense nights

Enable: `/settings` → 💞 Lovers, or `/set lovers on`

---

## Win Conditions

| Faction | Wins when |
|---------|-----------|
| 🏘️ **Town** | Every mafioso **and** every Serial Killer is dead |
| 🔪 **Mafia** | Mafia ≥ everyone else **and** no Serial Killer alive |
| 🩸 **Serial Killer** | At parity with remaining players, no mafia alive |
| 🃏 **Jester** | Town lynches the Jester (personal win; game continues) |
| 🧥 **Survivor** | Alive at game end alongside whoever won |

**Important:** Mafia reaching parity does **not** end the game while a Serial Killer still lives — someone must eliminate the rival killer first.

---

## Settings & Presets

Settings are configured **in the lobby only**, **by the host only**, before `/begin`.

### How to configure

1. `/startgame` → lobby opens
2. Tap **⚙️ Configure** or `/settings`
3. Pick a **preset**, tap toggles to cycle, or use **`/set`**
4. Tap **✅ Done**
5. `/begin` — settings lock

Saved settings become the default for the **next** game in that group.

### Presets

| Preset | Description | Key differences from Classic |
|--------|-------------|------------------------------|
| 🎭 **Classic** | Balanced default | See default timers above |
| ⚡ **Speed** | Quick game | Lobby 180s, Night 45s, Day 60s, Vote 30s, Last words 10s |
| 🎲 **Chaos** | Maximum chaos | 2× special roles, Lovers on, night kills revealed, Doctor self-heal on |
| 🏅 **Ranked** | Strict competitive | No reveals, no last words, no skip vote, no Night 1 kill, no ghost chat |

### Every setting

| Key | Type | Default (Classic) | Range / values | What it does |
|-----|------|-------------------|----------------|--------------|
| `night` | seconds | 90 | 20–300 | Time for night actions |
| `discussion` | seconds | 120 | 30–600 | Day debate before vote |
| `voting` | seconds | 60 | 15–180 | Time to cast ballots |
| `lobby` | seconds | 300 | 60–1800 | How long lobby stays open |
| `reveal_lynch` | on/off | on | on/off | Show lynched player's role |
| `reveal_night` | on/off | off | on/off | Show night victim's role |
| `majority` | on/off | on | on/off | Require >50% vote weight to lynch |
| `no_lynch` | on/off | on | on/off | Allow "Skip Today" vote |
| `last_words` | on/off | on | on/off | Condemned player speaks before dying |
| `first_night_kill` | on/off | on | on/off | Mafia can kill on Night 1 |
| `nomination` | on/off | off | on/off | Trial mode: nominate + second |
| `lovers` | on/off | off | on/off | Pair two lovers who die together |
| `mafia_chat` | on/off | on | on/off | `/mafia` private team chat at night |
| `ghost_chat` | on/off | on | on/off | `/ghost` chat for eliminated players |
| `live_board` | on/off | on | on/off | One updating vote message |
| `reactions` | on/off | on | on/off | Day mood bar on announcements |
| `doctor_self` | on/off | off | on/off | Doctor can protect themselves |
| `special_roles` | number | 3 | 2–6 | Lower = more special roles |

### Custom values (`/set`)

```
/set night 75
/set discussion 150
/set voting 45
/set lobby 420
/set lovers on
/set special_roles 2
```

Type `/set` alone for the full list. Invalid combinations are rejected.

---

## All Commands (Detailed)

Every bot command, how to type it, who can use it, when it works, and what happens.

### Targeting players (`@mention` or reply)

Many commands need a player target. You can specify them **two ways**:

1. **@mention** — type the player's Telegram username:
   ```
   /accuse @alice
   /kick @bob
   /host @carol
   ```

2. **Reply** — reply to one of their messages in the group (no `@` needed):
   ```
   (reply to Alice's message)
   /accuse

   (reply to Bob's message)
   /whisper I trust you — meet me at the well
   ```

The bot resolves `@username` against players **in the current game**. If the username doesn't match anyone in the roster, the command fails with a usage hint.

---

### `/start` — register with the bot

| | |
|---|---|
| **Where** | DM only (private chat with the bot) |
| **Who** | Everyone, once per account |
| **When** | Before your first game |

**Syntax**
```
/start
```

**Example**
```
You → Bot (DM): /start
Bot → You: You're all set! You can now join Mafia games in group chats.
```

**Why it matters:** Telegram blocks bots from messaging you until you start them. If you `/join` a lobby without doing this first, the group bot will ask you to DM `/start` before you can join.

**Does not work in groups** — only in DM.

---

### Group — setup & lobby

These run in the **group chat** while a lobby is open (before `/begin`), unless noted.

#### `/startgame` — open a lobby

| | |
|---|---|
| **Where** | Group only |
| **Who** | Anyone |
| **When** | No game already running in this group |

**Syntax**
```
/startgame
```

**Example**
```
Alice: /startgame
Bot: 🎭 Lobby card appears with Join Lobby, Configure buttons
     Host: Alice · 1/20 players · Mode: Classic
```

**What happens**
- You become **host** and are auto-joined
- Lobby timer starts (default 5 minutes)
- Waitlisted players (if any) get pinged for the new game

**Errors**
- In DM → "Use this command in a group chat."
- Game already running → "A game is already in progress. Use /status to check."

**Note:** Running `/startgame` clears any pending `/schedule` for this group.

---

#### `/schedule` — plan a future lobby

| | |
|---|---|
| **Where** | Group only |
| **Who** | Anyone (you become host when the lobby opens) |
| **When** | No game currently running |

**Syntax**
```
/schedule in 2h
/schedule in 45m
/schedule in 1d
/schedule at 20:00              ← 24-hour UTC, next occurrence
/schedule                         ← show pending schedule
/schedule cancel                  ← remove schedule (host or admin)
```

**Examples**
```
Alice: /schedule in 2h
Bot: 🗓️ Game scheduled for Wed Sep 2, 20:04 UTC (in 2 hours).
     You'll host when the lobby opens. Configure with /settings after it starts.
     /schedule to check · /schedule cancel to remove

Alice: /schedule
Bot: 🗓️ Scheduled game
     Host: @alice
     When: Wed Sep 2, 20:04 UTC (in 1 hour 58 minutes)

Alice: /schedule cancel
Bot: 🗓️ Scheduled game cancelled.
```

**When the timer fires**
```
Bot: 🗓️ Scheduled game time!
     @alice is hosting. Tap Join Lobby or /join.
     (Lobby card appears — same as /startgame)
```

**Time formats**

| Format | Example | Meaning |
|--------|---------|---------|
| Relative minutes | `/schedule in 30m` | 30 minutes from now |
| Relative hours | `/schedule in 2h` | 2 hours from now |
| Relative days | `/schedule in 1d` | 1 day from now |
| Absolute (UTC) | `/schedule at 20:00` | Next 20:00 UTC (today or tomorrow) |

**Limits**
- At least **5 minutes** in the future
- At most **7 days** in the future
- One schedule per group — a new `/schedule` replaces the old one

**Errors**
- In DM → "Use /schedule in your group chat…"
- Game running → "Finish or /endgame it before scheduling the next one."
- Bad time → parse error with usage hint
- Cancel by non-host → "Only the scheduled host or a group admin can cancel."

**Tips**
- DM `/start` before the scheduled time so you can receive your role as host
- `/startgame` manually clears a pending schedule if plans change early
- If the group is mid-game when the time hits, the schedule is skipped with a message

---

#### `/join` — join the lobby

| | |
|---|---|
| **Where** | Group only |
| **Who** | Anyone who DM'd `/start` |
| **When** | Lobby phase only |

**Syntax**
```
/join
```

Or tap **Join Lobby** on the lobby card.

**Example**
```
Bob: /join
Bot: Bob joins — lobby card updates to 2/20
```

**If no lobby is open**
```
Bob: /join
Bot: No active game. You've been added to the waitlist for the next game.
```

**If game already started**
```
Bob: /join
Bot: @bob, this game already started — you can't join mid-game.
     You'll be notified when the next one opens.
```

**Errors**
- Never DM'd `/start` → bot asks you to DM `/start` first

---

#### `/leave` — leave the lobby

| | |
|---|---|
| **Where** | Group |
| **Who** | Any joined player |
| **When** | Lobby phase (before roles are dealt) |

**Syntax**
```
/leave
```

**Example**
```
Bob: /leave
Bot: Bob left the lobby — player count drops
```

---

#### `/begin` — start the game

| | |
|---|---|
| **Where** | Group |
| **Who** | Host, or group admin |
| **When** | Lobby phase, ≥5 players |

**Syntax**
```
/begin
```

**Example**
```
Alice (host, 6 players joined): /begin
Bot: Roster announcement → role DMs sent to every player
```

**Requirements**
- At least **5** players in the lobby
- Every player must have **DM'd `/start`** (blocked players are removed and roles redealt before Night 1)

**Errors**
- Not host/admin → "Only the host or a group admin can start the game."
- Too few players → "Not enough players. Need at least 5, have 3."
- Already started → "The game has already begun."

**Note:** Group admins can `/begin` even if they aren't host. Settings lock at this moment.

---

#### `/settings` — open settings panel

| | |
|---|---|
| **Where** | Group (lobby only) |
| **Who** | **Host only** |
| **When** | Lobby phase, before `/begin` |

**Syntax**
```
/settings
```

Or tap **⚙️ Configure** on the lobby card.

**Example**
```
Alice (host): /settings
Bot: Inline panel — presets, timer toggles, feature toggles, ✅ Done
```

**What you can do in the panel**
- Pick a preset (Classic, Speed, Chaos, Ranked)
- Tap any setting to **cycle** through preset values
- Tap **✅ Done** to save

**Errors**
- Not in lobby → "No lobby is open. Run /startgame first…"
- Not host → "Only the host can configure this game."
- In DM → "Configure the game in your group lobby with /settings…"

Settings **lock** when `/begin` runs. Saved choices become the default for the **next** game in this group.

---

#### `/set <key> <value>` — custom setting value

| | |
|---|---|
| **Where** | Group (lobby only) |
| **Who** | **Host only** |
| **When** | Lobby phase |

**Syntax**
```
/set <key> <value>
/set                    ← lists all keys and ranges
```

**Timer examples**
```
/set night 75
/set discussion 150
/set voting 45
/set lobby 420
/set special_roles 2
```

**Toggle examples** (accepts `on`/`off`, `yes`/`no`, `true`/`false`, `1`/`0`)
```
/set lovers on
/set reveal_lynch off
/set first_night_kill on
/set nomination on
/set doctor_self on
/set ghost_chat off
```

**Full key list**

| Key | Type | Range / values |
|-----|------|----------------|
| `night` | seconds | 20–300 |
| `discussion` | seconds | 30–600 |
| `voting` | seconds | 15–180 |
| `lobby` | seconds | 60–1800 |
| `special_roles` | number | 2–6 (lower = more specials) |
| `reveal_lynch` | toggle | on/off |
| `reveal_night` | toggle | on/off |
| `majority` | toggle | on/off |
| `no_lynch` | toggle | on/off |
| `last_words` | toggle | on/off |
| `first_night_kill` | toggle | on/off |
| `nomination` | toggle | on/off (trial mode) |
| `lovers` | toggle | on/off |
| `mafia_chat` | toggle | on/off |
| `ghost_chat` | toggle | on/off |
| `live_board` | toggle | on/off |
| `reactions` | toggle | on/off |
| `doctor_self` | toggle | on/off |

**Example session**
```
Alice (host): /set night 75
Bot: ✅ Set night → 75

Alice: /set lovers on
Bot: ✅ Set lovers → on

Alice: /set night 10
Bot: Couldn't set night: minimum is 20
```

---

#### `/host @player` — transfer hosting

| | |
|---|---|
| **Where** | Group |
| **Who** | Current host, or group admin |
| **When** | Any non-finished game (lobby or in progress) |

**Syntax**
```
/host @player
/host          ← reply to their message instead
```

**Example**
```
Alice (host): /host @bob
Bot: 👑 Host transferred to @bob.
```

**Errors**
- Not host/admin → "Only the current host or a group admin can transfer host."
- Target not in game → "That player is not in this game."
- Target is 📵 silent/dead → "can't host — they are no longer active"

**Note:** New host can configure settings **only if still in lobby** (before `/begin`).

---

#### `/endgame` — force-end the game

| | |
|---|---|
| **Where** | Group |
| **Who** | Host, or group admin |
| **When** | Any active game |

**Syntax**
```
/endgame
```

**Example**
```
Alice (host): /endgame
Bot: 🛑 The game has been ended by the host.
     No winner declared.
```

**Stats impact:** Cancelled games do **not** count as wins or losses and do **not** break streaks.

---

### Group — during the game

#### `/status` — live game state

| | |
|---|---|
| **Where** | Group |
| **Who** | Anyone |
| **When** | Anytime a game exists |

**Syntax**
```
/status
```

**Example (in progress)**
```
Anyone: /status
Bot: 📊 Game Status
     Phase: discussion · Day 2 · Host: @alice
     ⏳ 87s left
     Alive (6): 🟢 @bob · 🟢 @carol · 📵 @dave …
     Dead (2): 💀 @eve — 🔍 Detective
```

**In lobby:** `/status` re-shows the lobby card instead.

**Player markers**
- 🟢 — alive and can act
- 📵 — silent (blocked bot / unreachable; can't vote or act)

---

#### `/graveyard` — death order

| | |
|---|---|
| **Where** | Group |
| **Who** | Anyone |
| **When** | After at least one death |

**Syntax**
```
/graveyard
```

**Example**
```
Anyone: /graveyard
Bot: ⚰️ Graveyard (oldest first)
     1. @eve — Night 1 (role shown if reveal setting enabled)
     2. @frank — Lynch Day 2
```

---

#### `/roles` — role reference

| | |
|---|---|
| **Where** | Group or DM |
| **Who** | Anyone |
| **When** | Anytime |

**Syntax**
```
/roles
```

**Example**
```
Anyone: /roles
Bot: Full list of all 14 roles with emoji and one-line descriptions
```

For deep detail on one role: `/help detective`, `/help godfather`, etc.

---

#### `/accuse @player` — public accusation

| | |
|---|---|
| **Where** | Group |
| **Who** | Alive players who can act (not 📵) |
| **When** | Discussion or nomination phase |

**Syntax**
```
/accuse @player
/accuse          ← reply to their message
```

**Example**
```
Bob: /accuse @alice
Bot: 👉 @bob accuses @alice! (2/5 accusations)

(When majority of eligible players have accused someone:)
Bot: ⚠️ @alice has been accused by the majority!
     @alice, use /defend to make your case.
```

**Rules**
- Cannot accuse yourself
- Cannot accuse the same person twice
- Can accuse 📵 silent players (they can still be lynched)
- Accusation count is tracked; crossing majority triggers a public nudge

---

#### `/defend [statement]` — formal defense

| | |
|---|---|
| **Where** | Group |
| **Who** | Alive players who can act |
| **When** | Discussion or nomination phase |
| **Limit** | **Once per day** |

**Syntax**
```
/defend I am town because I investigated Bob last night and...
```

**Example**
```
Alice (under fire): /defend I am innocent — I was the one who pushed the vote on Eve yesterday.
Bot: 🛡️ Defense from @alice:
     "I am innocent — I was the one who pushed the vote on Eve yesterday."
```

**Errors**
- Empty statement → "Usage: /defend I am innocent because..."
- Already defended today → "you've already made your defense today"
- Max **500 characters**

---

#### `/whisper @player [message]` — private daytime message

| | |
|---|---|
| **Where** | Group (message delivered in DM) |
| **Who** | Alive players who can act |
| **When** | Discussion or nomination phase only |
| **Limit** | **200 characters** per whisper |

**Syntax**
```
/whisper @bob I think Carol is mafia
/whisper I trust you          ← reply to Bob's message (no @ needed)
```

**Example**
```
Alice: /whisper @bob I think Carol is mafia
Bot (group): 🤫 @alice whispered something to @bob...
Bot (Bob DM): 🤫 Whisper from @alice: I think Carol is mafia
Bot (Alice DM): 🤫 Whisper sent to @bob.
```

**Rules**
- Group sees **that** a whisper happened, **not** the text
- Recipient must be alive and reachable (📵 players can't receive whispers)
- Cannot whisper yourself
- At night → DM reply: "Whispers are only allowed during the day discussion."

---

#### `/reveal` — Mayor goes public

| | |
|---|---|
| **Where** | Group |
| **Who** | **Mayor only**, alive and reachable |
| **When** | Discussion, nomination, or voting phase |
| **Limit** | **Once per game** |

**Syntax**
```
/reveal
```

**Example**
```
Carol (Mayor): /reveal
Bot: 🏛️ @carol steps forward as the MAYOR!
     Their vote now counts as 3.
```

**Errors** (sent to your DM)
- Not Mayor → "Only the Mayor can reveal themselves."
- Already revealed → "You have already revealed yourself."
- At night → "You can only reveal yourself during the day."

**Trade-off:** Your vote weighs **3**, but everyone knows you're Mayor — and mafia know exactly who to kill.

---

#### `/nominate @player` — put someone on trial

| | |
|---|---|
| **Where** | Group |
| **Who** | Alive players who can act |
| **When** | Discussion or nomination phase |
| **Requires** | `nomination` setting **on** (trial mode) |

**Syntax**
```
/nominate @player
/nominate          ← reply to their message
```

**Example**
```
Bob: /nominate @alice
Bot: ⚠️ @bob nominates @alice for trial!
     Someone must /second this nomination.
     (Nomination timer starts — default 45s)
```

**Rules**
- Cannot nominate yourself
- One nomination per target at a time
- If nobody `/second`s before timer → day ends with **no lynch**

---

#### `/second @player` — second a nomination

| | |
|---|---|
| **Where** | Group |
| **Who** | Alive players who can act (not the nominator) |
| **When** | Nomination phase only |
| **Requires** | Trial mode |

**Syntax**
```
/second @alice          ← @alice is the nominated player
/second                 ← reply to nominated player's message
```

**Example**
```
Carol: /second @alice
Bot: ⚖️ @carol seconds the nomination! @alice is now on trial.
     Vote guilty (lynch) or innocent (skip). 4 votes needed. 60 seconds.
     (Live vote board appears)
```

**Errors**
- No active nomination → "There is no active nomination for that player."
- Nominator tries to second own nomination → "You cannot second your own nomination."

---

#### `/kick @player` — remove a player

| | |
|---|---|
| **Where** | Group |
| **Who** | Host, or group admin |
| **When** | Lobby or any in-game phase |

**Syntax**
```
/kick @player
/kick          ← reply to their message
```

**Example (lobby)**
```
Alice (host): /kick @afk_guy
Bot: @afk_guy was removed from the lobby by the host
```

**Example (mid-game)**
```
Alice (host): /kick @afk_guy
Bot: 🚪 @afk_guy has been kicked from the game by the host.
     (If they had a lover: 💔 partner dies of grief)
```

**In lobby:** player is removed from roster.  
**In game:** player is marked dead (kicked); votes and night actions cleared.

---

### Voting (inline buttons, not a slash command)

During **voting phase**, the bot posts a **live vote board** — one message updated in place.

**How to vote**
- Tap a player's name on the vote board to vote for them
- Tap another name to **change** your vote
- Tap **🕊️ Skip Today** if `no_lynch` is enabled

**Example board**
```
🗳️ Voting — Day 2
@alice  ████░░ 3
@bob    ██░░░░ 1
🕊️ Skip  ░░░░░░ 0
Turnout: 4/6 · ⏳ 42s left
```

No `/vote` command — voting is always via **inline buttons**.

---

### Stats & history

#### `/stats` — player record

| | |
|---|---|
| **Where** | Group or DM |
| **Who** | Anyone |
| **Target** | Yourself, or another player via mention/reply |

**Syntax**
```
/stats
/stats @player
/stats          ← reply to someone's message
```

**Example**
```
Anyone: /stats
Bot: 📊 @alice's Record
     🔰 Newcomer · 12 games · 7 wins (58%)
     Streak: 2 · Best: 4
     Roles: Detective ×3 (2W) · Villager ×5 (3W) …
```

**Example (check someone else)**
```
Bob: /stats @alice
Bot: (Alice's full record card)
```

---

#### `/leaderboard` — top players

| | |
|---|---|
| **Where** | Group or DM |
| **Who** | Anyone |

**Syntax**
```
/leaderboard              ← this group's top 10 (in a group)
/leaderboard global       ← worldwide top 10 (works anywhere)
/top                      ← alias for /leaderboard
```

**Examples**
```
(in group)
Anyone: /leaderboard
Bot: 🏆 Top players here
     1. @alice — 7W · 58%
     2. @bob — 5W · 71%
     …

(in group, want global board)
Anyone: /leaderboard global
Bot: 🏆 Top players overall
     …

(in DM — defaults to global)
You: /leaderboard
Bot: 🏆 Top players overall
```

---

#### `/achievements` — unlock progress

| | |
|---|---|
| **Where** | Group or DM |
| **Who** | Yourself only |

**Syntax**
```
/achievements
```

**Example**
```
Anyone: /achievements
Bot: 🏅 Achievements
     ✅ 🥇 First Blood on the Board — Win a game
     ✅ 🎬 Welcome to Town — Finish your first game
     🔒 🔥 Unstoppable — Win 7 games in a row
     🔒 💔 Star-Crossed — (secret, hidden until earned)
```

---

#### `/lastgame` — previous game recap

| | |
|---|---|
| **Where** | Group only |
| **Who** | Anyone |

**Syntax**
```
/lastgame
/recap          ← alias
```

**Example**
```
Anyone: /lastgame
Bot: Full recap — winner, every role, timeline, awards
```

**In DM** → "Ask for the recap in the group whose game you want to see."

---

### Help commands

| Command | What you get |
|---------|--------------|
| `/help` | Help menu — list of topics |
| `/help general` | Setup, flow, tips (aliases: `game`, `play`, `howto`) |
| `/help settings` | Presets, toggles, `/set` reference |
| `/help commands` | Short command list |
| `/help gameplay` | Phases and win conditions |
| `/help stats` | Leaderboard and achievements |
| `/help roles` | Role name index |
| `/help detective` | One role explained (any role name works) |
| `/help lovers` | Lovers modifier |
| `/guide` | Link to this complete document on GitHub |

**Examples**
```
/help
/help godfather
/help settings
/guide
```

Role aliases work too: `/help doc` → Doctor, `/help cop` → Detective, `/help gf` → Godfather.

---

### DM-only commands

These **must** be sent in a **private chat** with the bot, not in the group.

#### `/myrole` — your role card

**Syntax**
```
/myrole
```

**Example**
```
You → Bot (DM): /myrole
Bot: Your role: Detective (town team)
     Status: Alive
```

**Errors**
- In group → "Use this command in DM with me!"
- Roles not dealt yet → "Roles haven't been dealt yet — hold tight!"
- Not in a game → "You're not in any active game."

---

#### `/mafia <message>` — mafia team chat

| | |
|---|---|
| **Who** | Mafia members only |
| **When** | Night phase (and role-assign phase) |
| **Requires** | `mafia_chat` setting on |

**Syntax**
```
/mafia Let's hit Carol tonight
/mafia Bob is claiming detective — don't trust him
```

**Example**
```
You → Bot (DM): /mafia I vote Carol
Bot: 🔪 Sent to your family.
Teammate DM: 🔪 @you: I vote Carol
```

**Errors**
- In group → "Send /mafia to me in a private chat, not in the group."
- Not mafia → "Only the mafia can use this channel."
- During day → "🌙 Mafia chat is only open at night."
- Setting off → "Mafia chat is disabled in this game."
- Max **400 characters**

---

#### `/ghost <message>` — eliminated players' chat

| | |
|---|---|
| **Who** | Dead players only |
| **When** | Anytime after elimination |
| **Requires** | `ghost_chat` setting on |

**Syntax**
```
/ghost I was the Doctor — Bob is lying
/ghost Called it, Alice was mafia all along
```

**Example**
```
You → Bot (DM, dead): /ghost Bob is definitely mafia
Bot: 👻 Sent to 3 other ghost(s).
Other dead players' DMs: 👻 @you (dead): Bob is definitely mafia
```

**Errors**
- Still alive → "Ghost chat is for the dead. You are still very much alive."
- Setting off → "Ghost chat is disabled in this game."
- Max **400 characters**

**Rule:** Never use the **group chat** after you're dead — use `/ghost` or stay silent.

---

### Night actions (DM inline buttons)

Power roles don't use slash commands at night. The bot DMs you **inline buttons** when it's your turn.

**How it works**
1. Night phase begins — group goes quiet
2. Bot DMs eligible roles an action keyboard
3. Tap a player name to choose your target
4. Tap a **different** name to change — **last tap before timer wins**
5. When timer ends (or everyone submits), night resolves

**Example (Detective DM)**
```
🔍 Investigation
Who do you want to look into tonight?
[ @alice ] [ @bob ] [ @carol ] …
```

**Example (Mafia DM)**
```
🔪 Mafia Kill
Agree with your team on tonight's victim:
[ @alice ] [ @bob ] [ @carol ] …
(Teammates see each other's selections update live)
```

**Roles with night buttons:** Mafia, Godfather, Framer, Detective, Doctor, Bodyguard, Escort, Lookout, Vigilante, Serial Killer.

**Roles without night actions:** Villager, Mayor, Jester, Survivor.

**Mafia kill rule:** All mafia must pick the **same** target or **nobody dies** that night.

**Vigilante:** One bullet for the **entire game** — choose carefully.

---

### Lobby inline buttons

On the lobby card:

| Button | Who | Action |
|--------|-----|--------|
| **Join Lobby** | Anyone | Same as `/join` |
| **⚙️ Configure** | Host | Opens `/settings` panel |
| Preset buttons | Host | Switch preset (Classic, Speed, …) |
| Setting toggles | Host | Cycle individual settings |
| **✅ Done** | Host | Save and close settings |

After game ends:

| Button | Action |
|--------|--------|
| **🔄 Rematch** | Opens new lobby; tapper becomes host |
| **🏆 Leaderboard** | Shows group leaderboard |
| **📜 Recap** | Shows last game summary |

---

### Command quick reference

| Command | Where | Who |
|---------|-------|-----|
| `/start` | DM | Everyone (once) |
| `/startgame` | Group | Anyone |
| `/schedule [when\|cancel]` | Group | Anyone |
| `/join` | Group | Anyone |
| `/leave` | Group | Joined players |
| `/begin` | Group | Host / admin |
| `/settings` | Group lobby | Host |
| `/set key value` | Group lobby | Host |
| `/host @player` | Group | Host / admin |
| `/endgame` | Group | Host / admin |
| `/status` | Group | Anyone |
| `/graveyard` | Group | Anyone |
| `/roles` | Anywhere | Anyone |
| `/accuse @player` | Group day | Alive & reachable |
| `/defend text` | Group day | Alive & reachable |
| `/whisper @player msg` | Group day | Alive & reachable |
| `/reveal` | Group day | Mayor |
| `/nominate @player` | Group day | Alive & reachable (trial mode) |
| `/second @player` | Group nomination | Alive & reachable (trial mode) |
| `/kick @player` | Group | Host / admin |
| `/stats [@player]` | Anywhere | Anyone |
| `/leaderboard [global]` | Anywhere | Anyone |
| `/achievements` | Anywhere | Self |
| `/lastgame` | Group | Anyone |
| `/help [topic]` | Anywhere | Anyone |
| `/guide` | Anywhere | Anyone |
| `/myrole` | DM | In-game players |
| `/mafia msg` | DM | Mafia (night) |
| `/ghost msg` | DM | Dead players |
| Vote board taps | Group vote | Alive & reachable |

---

## Help System

| Need | Command |
|------|---------|
| Quick menu | `/help` |
| Full rules overview | `/help general` |
| Configure a lobby | `/help settings` |
| One role explained | `/help godfather`, `/help jester`, … |
| This complete document | `/guide` |

---

## Stats, Leaderboard & Achievements

### `/stats` — your record

Tracks across all games and groups:
- Games played, wins, losses, win rate, survival rate
- Current streak and best streak
- Per-role breakdown (times played, times won)
- Lifetime totals: saves, kills, correct checks, whispers, votes on evil

**Cancelled games** (`/endgame`) count as neither win nor loss and don't affect streaks.

### `/leaderboard` — ranking

Shows top **10** players. Scoped to the group, or `global` for all groups.

**Scoring formula** (hidden, used for sort order):
```
score = wins + (win_rate × confidence × 5) + (best_streak × 0.5)
```
where `confidence = min(games_played / 5, 1)` — so one lucky win doesn't beat a proven record.

Display shows: wins and win rate per player.

### Rank titles (from total wins)

| Wins | Title |
|------|-------|
| 0–2 | 🔰 Newcomer |
| 3–9 | 🌱 Apprentice |
| 10–24 | ⭐ Regular |
| 25–49 | 🏆 Veteran |
| 50–99 | 💎 Mastermind |
| 100+ | 👑 Legend |

### `/achievements` — permanent unlocks

| Achievement | How to earn |
|-------------|-------------|
| 🎬 Welcome to Town | Finish your first game |
| 🥇 First Blood on the Board | Win a game |
| 🎩 Hat Trick | Win 3 games in a row |
| 🔥 Unstoppable | Win 7 games in a row |
| 🎖️ Veteran | Play 50 games |
| 🕴️ Mastermind | Win 5 games as mafia |
| 🏛️ Pillar of the Town | Win 10 games for town |
| 🕯️ Sole Survivor | Be the only player left alive |
| 🧿 Untouchable | Win 10 games without dying |
| 🃏 Jester's Delight | Win as Jester (get lynched) |
| 🩸 Lone Wolf | Win as Serial Killer |
| 🛡️ Guardian | Prevent 10 kills across your games |
| 🔍 Bloodhound | Correctly identify 10 threats |
| ☠️ Executioner | Personally eliminate 20 players |
| 🩹 Out of the Gate | Be the first to die in a game |
| 😵 Wrong Place, Wrong Night | Die on Night 1 |
| 📣 The Mayor's Gambit | Win as Mayor |
| 💔 Star-Crossed *(secret)* | Die of lover's grief |
| 🕊️ Martyr *(secret)* | Die as Bodyguard saving someone |
| 🎭 Man of Many Faces | Play 8 different roles |
| 🤫 Conspirator | Send 50 whispers |
| 🎯 Sharpshooter | Vote to lynch evil 25 times |
| 🏅 Decorated | Collect 10 end-of-game awards |

Secret achievements are hidden until unlocked.

---

## End-of-Game Awards

Each finished game may grant **one award per category** to a single clear winner (ties = no award):

| Award | Given to |
|-------|----------|
| 🎯 Sharpest Eye | Most votes cast on genuine threats |
| 🛡️ Guardian Angel | Most kills prevented |
| ☠️ The Reaper | Most personal eliminations |
| 🗣️ Loudest Voice | Most discussion messages |
| 🤫 The Schemer | Most whispers sent |
| 🔍 Bloodhound | Most correct investigations |
| 🩸 First Blood | First player to die |
| 🕯️ Last One Standing | Sole survivor |
| 😶 Silent Type | Quietest surviving player (≤2 messages) |

---

## After the Game

The recap shows:
- Winner and win condition
- Every player's role
- Night-by-night timeline
- Awards
- Buttons: **🔄 Rematch**, **🏆 Leaderboard**, **📜 Recap**

---

## Silent & Unreachable Players

A player becomes 📵 **silent** when:
- They blocked the bot after the game started
- Role DMs failed transiently (network/rate limit)
- They left the chat but weren't kicked

**Silent players:**
- Cannot accuse, defend, whisper, nominate, second, or vote
- Cannot submit night actions
- Are **excluded from the vote threshold** (won't stall lynch)
- Can still be nominated, accused, and lynched
- Show as 📵 on `/status`

**Before Night 1:** blocked/no-`/start` players are **removed** and roles redealt.

**AFK during game:** Host or admin can `/kick`. Night actions default to none; missing players are named in warnings.

---

## Bot Restarts & Recovery

- Game state is **saved to MongoDB** after every action
- On restart, active games **resume automatically** with a "bot restarted" message
- Timers are re-armed; phase continues from where it left off
- Vote boards may be **re-posted** rather than edited (harmless)
- During role assign, **all role DMs are re-sent** (same roles)
- `/myrole`, `/mafia`, `/ghost` attach to the first active game the bot finds you in

---

## Strategy Tips

### Mafia
Blend in during the day. Use `/mafia` at night to agree on one target — split votes kill nobody. Deflect suspicion onto active town players.

### Town
Track whispers, accusations, and who stays silent. Cross-reference Detective results across nights (remember Framer exists).

### Detective
Don't reveal results early. A Framer can make an innocent look guilty for exactly one night.

### Doctor
Protect high-value targets, not always the loudest claimer. Self-heal only if `doctor_self` is on.

### Bodyguard
You die saving someone — guard players likely to be night-killed. Only stops one attack per night.

### Lookout
Stake out players likely to be visited (claimed power roles, Mayor after reveal).

### Mayor
Reveal when your 3-weight vote will actually decide the lynch. You become the obvious night target.

### Vigilante
One shot. Confirm alignment if possible before firing.

### Jester
Be suspicious enough to lynch, not so obvious that the town catches on.

### Serial Killer
Mafia are rivals, not allies. Let town weaken them, then strike.

### Survivor
Stay neutral, stay quiet, stay alive. Don't become a convenient lynch.

---

## Etiquette

- 🚫 **Don't screenshot your role DM** into the group
- 🚫 **Don't carry grudges** between games to meta-target someone
- ✅ **Bluffing is encouraged** — claim any role, fabricate results
- ✅ **Dead players: use `/ghost`**, not the group chat
- ✅ **Have fun** — it's a party game

---

## FAQ

**Q: The bot won't DM me!**
A: DM the bot and send `/start` first. Telegram blocks unsolicited bot messages.

**Q: Can I be in multiple games at once?**
A: Yes, in different groups. `/myrole`, `/mafia`, and `/ghost` go to the first active game found — stick to one table if you can.

**Q: What happens if someone goes AFK?**
A: Night action defaults to none; warnings name missing players. Host/admin can `/kick`.

**Q: Someone blocked the bot / never got their role.**
A: Before Night 1: removed and roles redealt. After game started: marked 📵 silent — keeps seat, cannot act.

**Q: What if the bot restarts mid-game?**
A: Games resume from MongoDB. Timers re-arm; role DMs may be re-sent during role assign.

**Q: My investigation said Mafia but they were a Villager!**
A: A **Framer** was in play, or you checked someone framed that night. Framing lasts one night.

**Q: Can Doctor and Bodyguard protect the same person?**
A: Yes. Doctor stops the kill outright. Bodyguard only acts if Doctor didn't save.

**Q: Mafia had parity but the game didn't end?**
A: A **Serial Killer** was still alive. Eliminate all rival killers first.

**Q: Do dead players' votes count?**
A: No. Only living, reachable players count toward the lynch threshold.

**Q: Who can change settings?**
A: Only the **lobby host**, before `/begin`. Not admins, not other players.

**Q: How do I set custom timer values?**
A: Host types `/set night 75` (etc.) in the lobby. See [Every setting](#every-setting) for keys and ranges.

**Q: Can I schedule a game for later?**
A: Yes — `/schedule in 2h` or `/schedule at 20:00` (UTC). The bot opens a lobby automatically at that time; you become host. Use `/schedule` to check or `/schedule cancel` to remove it.

**Q: Where is the full guide?**
A: Type `/guide` in Telegram, or read this file on GitHub.
