# Mafia Bot — Player Guide

## What is Mafia?

Mafia (also called Werewolf) is a social deduction game. The town is infiltrated by the Mafia — a secret group of killers. During the day, everyone debates and votes to lynch a suspect. At night, the Mafia secretly eliminates a player. The town wins by finding and eliminating all Mafia members. The Mafia wins by reaching equal numbers with the town.

---

## How to Play

### Starting a Game

1. **Add the bot** to your Telegram group chat
2. **DM the bot** — send `/start` in a private message (required before joining any game)
3. In the group, type `/startgame` to create a new lobby
4. Others type `/join` to enter the lobby (minimum 5 players)
5. The host types `/begin` when enough players have joined

### Game Flow

```
LOBBY → NIGHT → DAY DISCUSSION → VOTING → NIGHT → ... → GAME OVER
```

Each game day has two phases that repeat until one team wins:

**🌙 Night Phase (90 seconds)**
- Everyone "sleeps" — the group goes quiet
- Special roles receive DM prompts to use their abilities
- The Mafia secretly chooses a victim

**☀️ Day Phase**
- *Discussion* (120 seconds) — Debate, accuse, defend, and whisper
- *Voting* (60 seconds) — Vote to lynch a suspected Mafia member

---

## Roles

### Town Team (good guys)

| Role | Ability |
|------|---------|
| 🏘️ **Villager** | No special ability. Use your wits and your vote! |
| 🔍 **Detective** | Each night, investigate one player — learn if they're Town or Mafia |
| 💊 **Doctor** | Each night, protect one player from being killed |
| 🔫 **Vigilante** | Once per game, kill a player at night (use wisely — you might hit a townie!) |

### Mafia Team (bad guys)

| Role | Ability |
|------|---------|
| 🔪 **Mafia** | Each night, vote with your team to eliminate one player |
| 🎩 **Godfather** | Same as Mafia, but appears *innocent* to the Detective! |

### Neutral

| Role | Ability |
|------|---------|
| 🃏 **Jester** | You win if the town votes to lynch YOU. Act suspicious! |

### Which roles appear?

Roles are randomly generated each game based on player count:
- **Mafia count** = ~25% of players (minimum 1)
- **Special roles** (Detective, Doctor, etc.) are randomly selected from an eligible pool — two games with the same players can have completely different role compositions!

---

## Commands

### Group Chat Commands

| Command | Who | Description |
|---------|-----|-------------|
| `/startgame` | Anyone | Open a new game lobby |
| `/join` | Anyone | Join the lobby |
| `/leave` | Anyone | Leave the lobby (before game starts) |
| `/begin` | Host | Start the game |
| `/endgame` | Host/Admin | Force-end the game |
| `/status` | Anyone | Show current phase, alive/dead count |
| `/accuse @player` | Alive | Publicly accuse someone during discussion |
| `/defend [text]` | Alive | Make your defense statement (once per day) |
| `/whisper @player [msg]` | Alive | Send a private whisper (group sees it happened!) |
| `/nominate @player` | Alive | Nominate someone for trial (nomination mode) |
| `/second @player` | Alive | Second a nomination |
| `/host @player` | Host | Transfer host to another player |
| `/kick @player` | Host | Remove an AFK player |

### DM Commands

| Command | Description |
|---------|-------------|
| `/start` | Register with the bot (required before joining) |
| `/myrole` | Re-send your current role |

### Night Actions (via DM buttons)

During the night, eligible roles receive inline buttons in their DM to select targets. You can change your choice before the timer expires — the last selection counts.

---

## Discussion Phase — How to Play It

The discussion phase is where the real game happens. Here's what you can do:

### 👉 Accuse (`/accuse @player`)
Publicly point the finger at someone. The bot tracks accusations and displays a tally. If a majority accuses one player, they're prompted to defend themselves.

### 🛡️ Defend (`/defend I was home all night...`)
Make a formal defense statement that the bot displays prominently. You only get ONE defense per day — make it count! Best used when you're being accused.

### 🤫 Whisper (`/whisper @player trust me, vote for Bob`)
Send a secret message to another player. They'll receive it as a DM. **But** — the group will see a notification that you whispered to them! This creates suspicion and visible alliances.

### 😶 Stay Silent
The bot tracks who speaks and who stays silent. At the end of discussion, silent players are called out — being quiet can make you look suspicious!

### Strategy Tips
- **Mafia**: Blend in. Accuse others to deflect suspicion. Use whispers to coordinate with teammates.
- **Town**: Watch who's quiet, who's deflecting, and who's whispering to whom.
- **Detective**: Don't reveal your role too early — the Mafia will kill you. Share info carefully.
- **Doctor**: Protect players who seem important. Don't always protect yourself.
- **Jester**: Act just suspicious enough to get lynched, but not so obvious that people catch on.

---

## Voting

After discussion ends, the bot posts voting buttons. You can:
- **Vote for a player** — tap their name to cast your vote
- **Vote "No Lynch"** — spare everyone (if enabled)
- **Change your vote** — tap a different button before time runs out

The player with the most votes is lynched. On a tie, no one is lynched.

### Last Words
When a player is voted out, they get 15 seconds to say their final words before execution. Use this to reveal information, make accusations, or go out with flair!

---

## Win Conditions

| Team | Wins When |
|------|-----------|
| 🏘️ **Town** | All Mafia members are eliminated |
| 🔪 **Mafia** | Mafia count ≥ Town count (they can't lose a vote) |
| 🃏 **Jester** | Gets voted out during the day (game continues for others) |

---

## Game Variants

The host can configure these options (set before `/begin`):

| Option | Default | Description |
|--------|---------|-------------|
| First Night Kill | On | If off, Mafia can't kill on Night 1 |
| Nomination System | Off | If on, requires `/nominate` + `/second` before voting |
| Allow No Lynch | On | Whether "No Lynch" is a voting option |
| Reveal Role on Death | On | Show dead player's role publicly |
| Last Words | On | Give lynched player time to speak |
| Doctor Self-Protect | Off | Whether Doctor can protect themselves |

---

## Tips & Etiquette

- 🚫 **Don't screenshot your DM** and share it in the group — that ruins the game
- 🚫 **Don't use information from a previous game** about someone's play style to unfairly target them
- ✅ **Bluffing is encouraged** — claim to be any role, lie about your investigation results, whatever it takes
- ✅ **Dead players should not talk** — once you're eliminated, watch silently
- ✅ **Have fun** — it's a party game! Don't take it too seriously

---

## FAQ

**Q: The bot won't DM me!**
A: You need to DM the bot first and send `/start`. Telegram doesn't allow bots to message users who haven't initiated contact.

**Q: Can I be in multiple games at once?**
A: Yes! The bot tracks games per group chat. Your DM actions are tied to the specific game via buttons.

**Q: What happens if someone goes AFK?**
A: Their night actions default to "no action." The host can `/kick` them. Silent players are called out during discussion summaries.

**Q: What if the bot crashes mid-game?**
A: Games are saved to the database after every action. On restart, all active games resume automatically.

**Q: Can spectators interfere?**
A: No. Only registered players can vote or submit actions. Spectators see public chat but can't interact with the game mechanics.
