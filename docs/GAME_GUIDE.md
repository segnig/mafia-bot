# Mafia Bot — Player Guide

## What is Mafia?

Mafia (also called Werewolf) is a social deduction game. The town is infiltrated by the Mafia — a secret group of killers. During the day, everyone debates and votes to lynch a suspect. At night, the Mafia secretly eliminates a player. The town wins by finding and eliminating every threat. The Mafia wins by reaching equal numbers with everyone else.

Depending on the group's settings there may also be a **Serial Killer** hunting alone, and neutrals who win on their own terms.

---

## How to Play

### Starting a Game

1. **Add the bot** to your Telegram group chat
2. **DM the bot** — send `/start` in a private message (required before joining any game)
3. In the group, type `/startgame` to create a new lobby
4. Others type `/join`, or tap **Join Lobby** on the card the bot posts
5. The host types `/begin` when enough players have joined (minimum 5)

The lobby card shows the host, the current player list, a fill bar, and which **mode** the game will use, so you know what you are joining before it starts. It stays open for 5 minutes — if enough players have joined by then it starts on its own, otherwise it is cancelled so it doesn't linger in the chat.

Everyone must have DMed the bot `/start` **before** `/begin`. If someone blocked the bot, they are removed and the roles are dealt again without them. If a message simply fails to send, they stay in the game but are marked silent (📵 on `/status`) and cannot act.

### Game Flow

```
LOBBY → ROLE DMs → NIGHT → DAY DISCUSSION → (trial) → VOTING → last words → NIGHT → … → GAME OVER
```

**✉️ Role assignment**
- The bot DMs everyone their role (and mafia teammates, and lovers) before Night 1
- The night does not start until those DMs have actually gone out
- If the bot restarts here, you get the same role again — a second copy is nothing to worry about

**🌙 Night Phase**
- The group goes quiet
- Roles with night abilities get inline buttons in their DM
- With mafia chat enabled, the mafia can plan privately with `/mafia`
- The bot names who it is still waiting on when time runs short

**☀️ Day Phase**
- *Discussion* — debate, accuse, defend, whisper, and tap the mood bar
- *Trial mode* (optional) — `/nominate` then `/second` to put someone on trial
- *Voting* — one vote board that updates in place as ballots come in
- *Last words* — a lynched player gets a few seconds before they are gone

---

## Roles

Send `/roles` at any time for the full in-chat reference.

### Town (good guys)

| Role | Ability |
|------|---------|
| 🏘️ **Villager** | No special power. Your vote and your voice are your weapons. |
| 🔍 **Detective** | Each night, investigate one player and learn their faction. |
| 💊 **Doctor** | Each night, protect one player from every kill that night. |
| 🛡️ **Bodyguard** | Each night, guard one player. If a killer comes, you take the blow — and take the attacker down with you. |
| 💃 **Escort** | Each night, occupy one player. Whatever they planned does not happen. |
| 🔭 **Lookout** | Each night, watch one player's house and learn exactly who came calling. |
| 🏛️ **Mayor** | Reveal yourself once with `/reveal`. Everyone learns who you are, but your vote counts as three. |
| 🔫 **Vigilante** | One bullet for the whole game, fired at night. Be sure. |

### Mafia (bad guys)

| Role | Ability |
|------|---------|
| 🔪 **Mafia** | Each night, agree with your team on a victim. Disagree and nobody dies. |
| 🎩 **Godfather** | Kills as mafia, but investigations come back **Town**. |
| 🖊️ **Framer** | Each night, plant evidence on one player so any investigation of them that night reports **Mafia**. |

### Killer

| Role | Ability |
|------|---------|
| 🩸 **Serial Killer** | You hunt alone. Kill every night, win when nobody can stop you. Reads as Mafia. |

### Neutral

| Role | Ability |
|------|---------|
| 🃏 **Jester** | You win if the town lynches **you**. Be impossible to ignore. |
| 🧥 **Survivor** | No side, no power. You win simply by being alive at the end. |

### 💞 Lovers

In some modes two players are secretly paired at the deal and told about each other. **When one dies, so does the other** — whatever their roles were. A mafioso paired with a townsperson has a very complicated night ahead.

### Which roles appear?

Roles are generated fresh each game from the player count:
- **Mafia count** ≈ 25% of players, minimum 1
- **Special roles** are drawn randomly from an eligible pool, and each role has a minimum player count before it can show up at all

Two games with the same players can have completely different compositions. The `chaos` mode roughly doubles the number of special roles.

---

## Commands

### Group Chat

| Command | Who | Description |
|---------|-----|-------------|
| `/startgame` | Anyone | Open a new game lobby |
| `/join` / `/leave` | Anyone | Join or leave the lobby before it starts |
| `/begin` | Host | Start the game |
| `/endgame` | Host/Admin | Force-end the game |
| `/status` | Anyone | Current phase, timer, alive and dead counts |
| `/graveyard` | Anyone | The dead, in the order they died |
| `/roles` | Anyone | Full role reference |
| `/settings` | Host (lobby only) | Configure rules before /begin |
| `/set <key> <value>` | Host (lobby only) | Set a custom value, e.g. `/set night 75` |
| `/accuse @player` | Alive & reachable | Publicly accuse someone (or reply to their message) |
| `/defend [text]` | Alive & reachable | Your defence statement (once per day) |
| `/whisper @player [msg]` | Alive & reachable | Private message — or reply to them; the group sees that it happened |
| `/reveal` | Mayor | Go public in exchange for vote weight |
| `/nominate` / `/second` | Alive & reachable | Put someone on trial (trial mode only) |
| `/host @player` | Host/Admin | Transfer host |
| `/kick @player` | Host/Admin | Remove an AFK player |

### Stats & History

| Command | Where | Description |
|---------|-------|-------------|
| `/stats` | Anywhere | Your lifetime record — reply to someone or mention them for theirs |
| `/leaderboard` | Group | Best players in this group |
| `/leaderboard global` | Anywhere | Best players overall |
| `/achievements` | Anywhere | What you've unlocked, and what's left |
| `/lastgame` | Group | Recap of this group's most recent game |

### Help

| Command | Where | Description |
|---------|-------|-------------|
| `/help` | Anywhere | Help menu — lists all topics |
| `/help general` | Anywhere | Full how-to: setup, flow, tips |
| `/help settings` | Anywhere | Presets, toggles, `/set` custom values |
| `/help roles` | Anywhere | Role index — then `/help detective`, etc. |
| `/help <role>` | Anywhere | One role explained in detail |
| `/guide` | Anywhere | Link to this full guide on GitHub |

### DM

| Command | Description |
|---------|-------------|
| `/start` | Register with the bot (required before joining) |
| `/myrole` | Re-send your role card |
| `/mafia <message>` | Talk to your mafia team — night only |
| `/ghost <message>` | Talk to the other dead players |

### Night Actions

Eligible roles get inline buttons in their DM. You can change your choice until the timer expires — the last selection counts. Your teammates see what you picked, so the mafia can actually coordinate.

---

## Discussion Phase — How to Play It

### 👉 Accuse (`/accuse @player`)
Publicly point the finger. The bot tracks accusations and shows a tally. If a majority accuses one player, they're prompted to defend themselves.

### 🛡️ Defend (`/defend I was home all night...`)
A formal statement the bot displays prominently. **One per day** — make it count.

### 🤫 Whisper (`/whisper @player trust me, vote Bob`)
A private DM to another player. You can also **reply** to their message and type `/whisper your secret`. **The group is told that you whispered to them**, though not what you said. Instant suspicion, visible alliances.

A player the bot cannot reach (blocked, left, 📵 on `/status`) cannot send or receive whispers, and cannot accuse, defend, nominate, or second. They can still be nominated and lynched.

### 🎭 React
Tap the mood bar under the day announcement. The tally appears in the day summary. One reaction each; you can change it.

### 🏛️ Reveal (Mayor only)
`/reveal` trades your anonymity for voting power. Your vote starts counting as three — and the mafia now know exactly where to strike. Day only.

### 😶 Stay Silent
The bot tracks who spoke. Silent players are named in the day summary — being quiet is itself a choice, and a visible one.

### Strategy Tips
- **Mafia**: Blend in. Deflect. Use `/mafia` at night to agree on a target before the clock runs out — a split vote kills nobody.
- **Town**: Watch who's quiet, who's deflecting, and who whispered to whom.
- **Detective**: Don't reveal early — and remember a **Framer** can make an innocent look guilty for exactly one night.
- **Doctor**: Protect who matters, not always the obvious claim.
- **Bodyguard**: You only absorb one attack per night, and you die doing it.
- **Lookout**: Watching a likely target beats watching a suspect.
- **Mayor**: Reveal when your vote will actually decide something. You are a target the moment you do.
- **Jester**: Suspicious enough to be lynched, not so obvious that people catch on.
- **Serial Killer**: The mafia are your rivals, not your allies. Let the town fight them.

---

## Voting

The bot posts one vote board and keeps rewriting it as ballots come in — the tally, who voted for whom, a bar per candidate, turnout, and the seconds left. Tap a name to vote, tap another to change it.

A lynch needs a **majority of the total voting weight** — more than half. With 8 ordinary players that's 5. A revealed Mayor raises both their own weight *and* the bar for everyone, so revealing does not simply hand them the day. If the leader falls short or the vote ties, nobody is lynched.

Players who have gone silent (blocked the bot, left the chat) don't count toward the total, so one dropout can't stall the round.

### Last Words
A player voted out gets a few seconds for their final words before execution. Reveal something, accuse someone, or go out with flair.

---

## Win Conditions

| Team | Wins When |
|------|-----------|
| 🏘️ **Town** | Every mafioso **and** every serial killer is dead |
| 🔪 **Mafia** | Mafia ≥ everyone else, and no serial killer is left alive |
| 🩸 **Serial Killer** | At parity with no mafia left |
| 🃏 **Jester** | The town lynches them — the game continues for everyone else |
| 🧥 **Survivor** | Alive at the end, alongside whoever won |

The mafia reaching parity does **not** end the game while a serial killer is still hunting them. Somebody still has to win it.

---

## After the Game

The recap shows every player's role, who won, a night-by-night timeline of how it happened, and the awards:

🎯 Sharpest Eye · 🛡️ Guardian Angel · ☠️ The Reaper · 🗣️ Loudest Voice · 🤫 The Schemer · 🔍 Bloodhound · 🩸 First Blood · 🕯️ Last One Standing · 😶 Silent Type

An award with no clear winner is skipped rather than handed out on a tie.

Then tap **🔄 Rematch** to go straight into another game, or **🏆 Leaderboard** / **📜 Recap** to look back.

### Progression

Every finished game updates your record: games, wins, losses, win rate, survival rate, current and best streak, and a per-role breakdown of what you actually win with. Wins move you up the ranks — 🔰 Newcomer, 🌱 Apprentice, ⭐ Regular, 🏆 Veteran, 💎 Mastermind, 👑 Legend.

**Achievements** unlock permanently: your first game, your first win, a hat trick, ten wins for the town, winning as the Serial Killer, dying on the very first night, and more. A few are secret and only appear once you've earned them.

A game **cancelled by the host counts as neither a win nor a loss**, and leaves your streak alone — so nobody can protect a record by walking away from a losing position.

---

## Game Modes & Settings

When someone runs `/startgame`, a **lobby** opens. **Only the host** configures the rules **before `/begin`**:

1. Tap **⚙️ Configure** on the lobby card, or type `/settings`
2. Pick a preset, tap options to cycle, or use **`/set <key> <value>`** for custom numbers
3. Tap **✅ Done** — settings lock once the host runs `/begin`

**Custom values** (host only, lobby only):

```
/set night 75
/set discussion 150
/set voting 45
/set lobby 420
/set lovers on
/set special_roles 3
```

Type `/set` alone for the full list and allowed ranges.

Settings are saved for the group, so the next `/startgame` starts from the same rules unless you change them again in the lobby.

| Preset | What it's for |
|--------|---------------|
| 🎭 **Classic** | The balanced default |
| ⚡ **Speed** | Half-length phases for a quick game |
| 🎲 **Chaos** | Every role in play, lovers, a serial killer, reveals on |
| 🏅 **Ranked** | Strict: no last words, no skipping, nothing revealed |

Individually tweakable: phase lengths (night, discussion, voting, lobby), reveal on lynch, reveal on night kill, majority to lynch, allow skipping, last words, night 1 kill, trial mode, lovers, mafia night chat, ghost chat, live vote board, day reactions, doctor self-heal, and special role density.

A combination that couldn't produce a playable game is refused rather than saved, so `/startgame` can always trust what it reads back.

---

## Tips & Etiquette

- 🚫 **Don't screenshot your DM** into the group — that ruins the game
- 🚫 **Don't carry grudges between games** to unfairly target someone
- ✅ **Bluffing is encouraged** — claim any role, lie about your results
- ✅ **Dead players: use `/ghost`**, not the group chat
- ✅ **Have fun** — it's a party game

---

## FAQ

**Q: The bot won't DM me!**
A: DM the bot first and send `/start`. Telegram doesn't allow bots to message users who haven't initiated contact.

**Q: Can I be in multiple games at once?**
A: Yes, in different groups. Night-action buttons always belong to one game. `/myrole`, `/mafia`, and `/ghost` go to the first active game the bot finds you in, so stick to one table if you can.

**Q: What happens if someone goes AFK?**
A: Their night action defaults to no action, and the night warning names who is still missing. The host or a group admin can `/kick` them. Silent players are called out in the day summary and do not count toward votes.

**Q: Someone blocked the bot / never got their role.**
A: If they blocked it (or never sent `/start`) **before Night 1**, they are removed and roles are dealt again. If a send just fails, or they block **after** the game has started, they stay on the roster as 📵 silent: they keep their seat, cannot act, and do not stall the vote.

**Q: What if the bot restarts mid-game?**
A: Game state is saved after every action and active games resume automatically. Live panels like the vote board may be re-posted rather than edited, which costs nothing. If the restart lands while roles are being dealt, everyone is sent their role again — the same role, so a second copy is nothing to worry about.

**Q: My investigation said Mafia but they were a Villager!**
A: A **Framer** was in play, or you checked someone who was framed that night. Framing lasts exactly one night.

**Q: Can a Doctor and a Bodyguard protect the same person?**
A: Yes. The Doctor's treatment stops the attack outright. The Bodyguard only steps in if the target isn't already saved — and only absorbs one attack per night.

**Q: The mafia had parity but the game didn't end?**
A: A Serial Killer was still alive. A faction only takes the game once no rival killer is left to take it from them.

**Q: Do dead players' votes count?**
A: No. Only living, reachable players count toward the lynch threshold.
