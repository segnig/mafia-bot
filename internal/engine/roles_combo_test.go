package engine

import (
	"crypto/rand"
	"strconv"
	"testing"
)

// Every legal table size has to produce a balanced, fully assigned game — not
// just the sizes the other tests happen to use.
func TestAllocateRolesIsBalancedAtEveryLegalSize(t *testing.T) {
	cfg := DefaultConfig()
	for n := cfg.MinPlayers; n <= cfg.MaxPlayers; n++ {
		n := n
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			ids := make([]PlayerID, n)
			for i := range ids {
				ids[i] = PlayerID(i + 1)
			}
			for trial := 0; trial < 40; trial++ {
				assignment, err := AllocateRoles(ids, cfg, rand.Reader)
				if err != nil {
					t.Fatalf("n=%d trial=%d: %v", n, trial, err)
				}
				if len(assignment) != n {
					t.Fatalf("n=%d assigned %d roles", n, len(assignment))
				}
				roles := make([]Role, 0, n)
				for _, pid := range ids {
					r, ok := assignment[pid]
					if !ok {
						t.Fatalf("player %d was not given a role", pid)
					}
					if _, known := roleCatalog[r]; !known {
						t.Fatalf("dealt unknown role %q", r)
					}
					roles = append(roles, r)
				}
				if err := ValidateBalance(roles, n); err != nil {
					t.Fatalf("n=%d trial=%d unbalanced: %v", n, trial, err)
				}
			}
		})
	}
}

// Force every optional role into the pool on its own so the catalog cannot
// contain a role the dealer refuses to emit.
func TestEveryOptionalRoleCanBeDealt(t *testing.T) {
	base := DefaultConfig()
	base.MinPlayers = 5
	base.MaxPlayers = 20
	base.SpecialRoleDivisor = 1 // take as many specials as the table can hold

	for _, def := range DefaultOptionalRoles() {
		def := def
		t.Run(string(def.Role), func(t *testing.T) {
			cfg := base
			cfg.OptionalRoles = []RoleDefinition{def}
			n := def.MinPlayers
			if n < cfg.MinPlayers {
				n = cfg.MinPlayers
			}
			ids := make([]PlayerID, n)
			for i := range ids {
				ids[i] = PlayerID(i + 1)
			}

			seen := false
			for trial := 0; trial < 80 && !seen; trial++ {
				assignment, err := AllocateRoles(ids, cfg, rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range assignment {
					if r == def.Role {
						seen = true
						break
					}
				}
			}
			if !seen {
				t.Fatalf("%s never appeared in %d deals at %d players", def.Role, 80, n)
			}
		})
	}
}

func TestVillagerAndMafiaAreAlwaysInThePool(t *testing.T) {
	cfg := DefaultConfig()
	n := cfg.MinPlayers
	ids := make([]PlayerID, n)
	for i := range ids {
		ids[i] = PlayerID(i + 1)
	}
	assignment, err := AllocateRoles(ids, cfg, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var mafia, town int
	for _, r := range assignment {
		switch RoleTeam(r) {
		case TeamMafia:
			mafia++
		case TeamTown:
			town++
		}
	}
	if mafia < 1 {
		t.Error("a legal game must have at least one mafia")
	}
	if town < 1 {
		t.Error("a legal game must have at least one town player")
	}
}
