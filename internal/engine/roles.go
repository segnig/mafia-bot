package engine

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
)

func ComputeMafiaCount(n int, divisor int) int {
	if divisor <= 0 {
		divisor = 4
	}
	count := n / divisor
	if count < 1 {
		count = 1
	}
	maxAllowed := (n - 1) / 2
	if count > maxAllowed {
		count = maxAllowed
	}
	return count
}

func SpecialRoleBudget(n int, divisor int) int {
	if divisor <= 0 {
		divisor = 3
	}
	budget := n / divisor
	return budget
}

func filterEligible(roles []RoleDefinition, n int) []RoleDefinition {
	var eligible []RoleDefinition
	for _, r := range roles {
		if n >= r.MinPlayers {
			eligible = append(eligible, r)
		}
	}
	return eligible
}

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
	return len(pool) - 1
}

func SampleOptionalRoles(eligible []RoleDefinition, budget int, rng io.Reader) []RoleDefinition {
	pool := append([]RoleDefinition{}, eligible...)
	var chosen []RoleDefinition
	for i := 0; i < budget && len(pool) > 0; i++ {
		totalWeight := 0.0
		for _, r := range pool {
			totalWeight += r.Weight
		}
		pick := weightedRandomIndex(pool, totalWeight, rng)
		chosen = append(chosen, pool[pick])
		pool = append(pool[:pick], pool[pick+1:]...)
	}
	return chosen
}

func ValidateBalance(roles []Role, n int) error {
	mafiaCount := 0
	villagerCount := 0
	for _, r := range roles {
		if RoleTeam(r) == TeamMafia {
			mafiaCount++
		}
		if r == RoleVillager {
			villagerCount++
		}
	}
	halfCeil := (n + 1) / 2
	if mafiaCount >= halfCeil {
		return fmt.Errorf("mafia count %d >= ceil(n/2)=%d", mafiaCount, halfCeil)
	}
	if villagerCount < 0 {
		return fmt.Errorf("negative villager count")
	}
	return nil
}

func GenerateRoleSet(n int, cfg GameConfig, rng io.Reader) ([]Role, error) {
	mafiaCount := ComputeMafiaCount(n, cfg.MafiaRatioDivisor)
	eligible := filterEligible(cfg.OptionalRoles, n)
	budget := SpecialRoleBudget(n, cfg.SpecialRoleDivisor)
	if budget > len(eligible) {
		budget = len(eligible)
	}
	chosen := SampleOptionalRoles(eligible, budget, rng)

	var roles []Role
	godfatherChosen := false
	for _, r := range chosen {
		if r.ReplacesMafiaSlot {
			godfatherChosen = true
			continue
		}
		roles = append(roles, r.Role)
	}

	mafiaRoles := make([]Role, mafiaCount)
	for i := range mafiaRoles {
		mafiaRoles[i] = RoleMafia
	}
	if godfatherChosen && mafiaCount >= 1 {
		mafiaRoles[0] = RoleGodfather
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
		return nil, err
	}
	return roles, nil
}

func MinimalSafeRoleSet(n int, cfg GameConfig) []Role {
	mafiaCount := ComputeMafiaCount(n, cfg.MafiaRatioDivisor)
	roles := make([]Role, n)
	for i := 0; i < mafiaCount; i++ {
		roles[i] = RoleMafia
	}
	for i := mafiaCount; i < n; i++ {
		roles[i] = RoleVillager
	}
	return roles
}

func FisherYatesShuffle(items []PlayerID, rng io.Reader) []PlayerID {
	shuffled := append([]PlayerID{}, items...)
	for i := len(shuffled) - 1; i > 0; i-- {
		jBig, err := rand.Int(rng, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(jBig.Int64())
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	return shuffled
}

func AllocateRoles(players []PlayerID, cfg GameConfig, rng io.Reader) (map[PlayerID]Role, error) {
	n := len(players)
	if n < cfg.MinPlayers {
		return nil, fmt.Errorf("not enough players: have %d, need %d", n, cfg.MinPlayers)
	}

	var roleSet []Role
	var err error
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		roleSet, err = GenerateRoleSet(n, cfg, rng)
		if err == nil {
			break
		}
	}
	if err != nil {
		roleSet = MinimalSafeRoleSet(n, cfg)
	}

	if len(roleSet) != n {
		return nil, fmt.Errorf("internal error: role set size %d != player count %d", len(roleSet), n)
	}

	shuffled := FisherYatesShuffle(players, rng)

	assignment := make(map[PlayerID]Role, n)
	for i, pid := range shuffled {
		assignment[pid] = roleSet[i]
	}
	return assignment, nil
}
