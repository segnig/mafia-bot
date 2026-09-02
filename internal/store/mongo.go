package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/stats"
)

type MongoStore struct {
	client      *mongo.Client
	db          *mongo.Database
	games       *mongo.Collection
	waitlists   *mongo.Collection
	dmConfirmed *mongo.Collection
	cooldowns   *mongo.Collection
	playerStats *mongo.Collection
	history     *mongo.Collection
	settings    *mongo.Collection
	scheduled   *mongo.Collection
}

func NewMongoStore(uri, dbName string) (*MongoStore, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	opts := options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(15 * time.Second).
		SetConnectTimeout(15 * time.Second)

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	db := client.Database(dbName)

	// Create indexes
	gamesCol := db.Collection("games")
	gamesCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "game_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	waitlistsCol := db.Collection("waitlists")
	waitlistsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "chat_id", Value: 1}},
	})

	dmCol := db.Collection("dm_confirmed")
	dmCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "player_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	cooldownsCol := db.Collection("cooldowns")
	cooldownsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "key", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	// TTL index: cooldowns expire after 30 seconds
	cooldownsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "created_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(30),
	})

	statsCol := db.Collection("player_stats")
	statsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "player_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	// The leaderboard is sorted in Go but the candidate set is fetched by
	// wins, so this index keeps that query cheap as the collection grows.
	statsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "wins", Value: -1}},
	})
	statsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "chat_ids", Value: 1}},
	})

	historyCol := db.Collection("game_history")
	historyCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "chat_id", Value: 1}, {Key: "ended_at", Value: -1}},
	})

	settingsCol := db.Collection("chat_settings")
	settingsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "chat_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	scheduledCol := db.Collection("scheduled_games")
	scheduledCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "chat_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	scheduledCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "scheduled_at", Value: 1}},
	})

	return &MongoStore{
		client:      client,
		db:          db,
		games:       gamesCol,
		waitlists:   waitlistsCol,
		dmConfirmed: dmCol,
		cooldowns:   cooldownsCol,
		playerStats: statsCol,
		history:     historyCol,
		settings:    settingsCol,
		scheduled:   scheduledCol,
	}, nil
}

func (m *MongoStore) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.client.Disconnect(ctx)
}

func (m *MongoStore) Save(state *engine.GameState) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	filter := bson.M{"game_id": string(state.ID)}
	update := bson.M{
		"$set": bson.M{
			"game_id":    string(state.ID),
			"chat_id":    state.ChatID,
			"phase":      string(state.Phase),
			"state_json": string(data),
			"updated_at": time.Now(),
		},
	}
	opts := options.Update().SetUpsert(true)
	_, err = m.games.UpdateOne(ctx, filter, update, opts)
	return err
}

func (m *MongoStore) Load(id engine.GameID) (*engine.GameState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc struct {
		StateJSON string `bson:"state_json"`
	}
	err := m.games.FindOne(ctx, bson.M{"game_id": string(id)}).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("game %s not found", id)
		}
		return nil, fmt.Errorf("find game: %w", err)
	}

	var state engine.GameState
	if err := json.Unmarshal([]byte(doc.StateJSON), &state); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}
	return &state, nil
}

func (m *MongoStore) Delete(id engine.GameID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.games.DeleteOne(ctx, bson.M{"game_id": string(id)})
	return err
}

func (m *MongoStore) ListActive() ([]engine.GameID, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"phase": bson.M{"$nin": []string{"idle", "game_over"}}}
	cursor, err := m.games.Find(ctx, filter, options.Find().SetProjection(bson.M{"game_id": 1}))
	if err != nil {
		return nil, fmt.Errorf("list active: %w", err)
	}
	defer cursor.Close(ctx)

	var ids []engine.GameID
	for cursor.Next(ctx) {
		var doc struct {
			GameID string `bson:"game_id"`
		}
		if err := cursor.Decode(&doc); err == nil {
			ids = append(ids, engine.GameID(doc.GameID))
		}
	}
	return ids, nil
}

func (m *MongoStore) AddToWaitlist(chatID int64, playerID engine.PlayerID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"chat_id": chatID}
	update := bson.M{"$addToSet": bson.M{"players": int64(playerID)}}
	opts := options.Update().SetUpsert(true)
	_, err := m.waitlists.UpdateOne(ctx, filter, update, opts)
	return err
}

func (m *MongoStore) GetWaitlist(chatID int64) ([]engine.PlayerID, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc struct {
		Players []int64 `bson:"players"`
	}
	err := m.waitlists.FindOne(ctx, bson.M{"chat_id": chatID}).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}

	ids := make([]engine.PlayerID, len(doc.Players))
	for i, p := range doc.Players {
		ids[i] = engine.PlayerID(p)
	}
	return ids, nil
}

func (m *MongoStore) ClearWaitlist(chatID int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.waitlists.DeleteOne(ctx, bson.M{"chat_id": chatID})
	return err
}

func (m *MongoStore) SetDMConfirmed(playerID engine.PlayerID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"player_id": int64(playerID)}
	update := bson.M{"$set": bson.M{"player_id": int64(playerID), "confirmed_at": time.Now()}}
	opts := options.Update().SetUpsert(true)
	_, err := m.dmConfirmed.UpdateOne(ctx, filter, update, opts)
	return err
}

func (m *MongoStore) IsDMConfirmed(playerID engine.PlayerID) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := m.dmConfirmed.FindOne(ctx, bson.M{"player_id": int64(playerID)}).Err()
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (m *MongoStore) SetJoinCooldown(chatID int64, playerID engine.PlayerID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := fmt.Sprintf("%d:%d", chatID, playerID)
	filter := bson.M{"key": key}
	update := bson.M{"$set": bson.M{"key": key, "created_at": time.Now()}}
	opts := options.Update().SetUpsert(true)
	_, err := m.cooldowns.UpdateOne(ctx, filter, update, opts)
	return err
}

func (m *MongoStore) HasJoinCooldown(chatID int64, playerID engine.PlayerID) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := fmt.Sprintf("%d:%d", chatID, playerID)
	err := m.cooldowns.FindOne(ctx, bson.M{"key": key}).Err()
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Player records, game history, and chat settings are stored as JSON strings
// in the same style as game state: the Go types stay the source of truth, and
// adding a field needs no migration. A handful of fields are duplicated as
// real BSON so Mongo can index and query them.

func (m *MongoStore) LoadPlayerStats(playerID engine.PlayerID) (*stats.PlayerStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc struct {
		StatsJSON string `bson:"stats_json"`
	}
	err := m.playerStats.FindOne(ctx, bson.M{"player_id": int64(playerID)}).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return stats.NewPlayerStats(playerID), nil
		}
		return nil, fmt.Errorf("find player stats: %w", err)
	}

	var s stats.PlayerStats
	if err := json.Unmarshal([]byte(doc.StatsJSON), &s); err != nil {
		return nil, fmt.Errorf("unmarshal player stats: %w", err)
	}
	return &s, nil
}

func (m *MongoStore) SavePlayerStats(s *stats.PlayerStats) error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal player stats: %w", err)
	}

	update := bson.M{
		"$set": bson.M{
			"player_id":   int64(s.PlayerID),
			"username":    s.Username,
			"wins":        s.Wins,
			"games":       s.GamesPlayed,
			"stats_json":  string(data),
			"last_played": s.LastPlayed,
		},
	}
	_, err = m.playerStats.UpdateOne(ctx,
		bson.M{"player_id": int64(s.PlayerID)}, update, options.Update().SetUpsert(true))
	return err
}

// topPlayerCandidates bounds how many records the leaderboard pulls back
// before ranking them in Go. The score blends win rate and streaks, so the
// top of the final board is not simply the top by wins — but a player far
// enough down the win list cannot reach the visible top either.
const topPlayerCandidates = 200

func (m *MongoStore) TopPlayers(chatID int64, limit int) ([]*stats.PlayerStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{}
	if chatID != 0 {
		filter["chat_ids"] = chatID
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "wins", Value: -1}}).
		SetLimit(topPlayerCandidates)

	cursor, err := m.playerStats.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("top players: %w", err)
	}
	defer cursor.Close(ctx)

	var all []*stats.PlayerStats
	for cursor.Next(ctx) {
		var doc struct {
			StatsJSON string `bson:"stats_json"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		var s stats.PlayerStats
		if err := json.Unmarshal([]byte(doc.StatsJSON), &s); err != nil {
			continue
		}
		all = append(all, &s)
	}

	ranked := stats.Leaderboard(all)
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

func (m *MongoStore) SaveGameRecord(record *stats.GameRecord) error {
	if record == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal game record: %w", err)
	}

	doc := bson.M{
		"game_id":     string(record.GameID),
		"chat_id":     record.ChatID,
		"ended_at":    record.EndedAt,
		"winner":      string(record.Winner),
		"days":        record.Days,
		"record_json": string(data),
	}
	if _, err := m.history.InsertOne(ctx, doc); err != nil {
		return fmt.Errorf("insert game record: %w", err)
	}

	// Tagging each participant with the chat is what makes a per-chat
	// leaderboard possible without a second collection.
	for _, p := range record.Players {
		_, err := m.playerStats.UpdateOne(ctx,
			bson.M{"player_id": int64(p.ID)},
			bson.M{
				"$addToSet": bson.M{"chat_ids": record.ChatID},
				"$setOnInsert": bson.M{
					"player_id": int64(p.ID),
				},
			},
			options.Update().SetUpsert(true))
		if err != nil {
			return fmt.Errorf("tag player %d with chat: %w", p.ID, err)
		}
	}
	return nil
}

func (m *MongoStore) LastGameRecord(chatID int64) (*stats.GameRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := options.FindOne().SetSort(bson.D{{Key: "ended_at", Value: -1}})
	var doc struct {
		RecordJSON string `bson:"record_json"`
	}
	err := m.history.FindOne(ctx, bson.M{"chat_id": chatID}, opts).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("find last game: %w", err)
	}

	var record stats.GameRecord
	if err := json.Unmarshal([]byte(doc.RecordJSON), &record); err != nil {
		return nil, fmt.Errorf("unmarshal game record: %w", err)
	}
	return &record, nil
}

func (m *MongoStore) LoadChatSettings(chatID int64) (*ChatSettings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc struct {
		SettingsJSON string `bson:"settings_json"`
	}
	err := m.settings.FindOne(ctx, bson.M{"chat_id": chatID}).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return NewChatSettings(chatID), nil
		}
		return nil, fmt.Errorf("find chat settings: %w", err)
	}

	var s ChatSettings
	if err := json.Unmarshal([]byte(doc.SettingsJSON), &s); err != nil {
		return nil, fmt.Errorf("unmarshal chat settings: %w", err)
	}
	if s.Overrides == nil {
		s.Overrides = make(map[string]string)
	}
	return &s, nil
}

func (m *MongoStore) SaveChatSettings(s *ChatSettings) error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.UpdatedAt = time.Now()
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal chat settings: %w", err)
	}

	update := bson.M{
		"$set": bson.M{
			"chat_id":       s.ChatID,
			"preset":        s.Preset,
			"settings_json": string(data),
			"updated_at":    s.UpdatedAt,
		},
	}
	_, err = m.settings.UpdateOne(ctx,
		bson.M{"chat_id": s.ChatID}, update, options.Update().SetUpsert(true))
	return err
}

func (m *MongoStore) SaveScheduledGame(sg *ScheduledGame) error {
	if sg == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := json.Marshal(sg)
	if err != nil {
		return fmt.Errorf("marshal scheduled game: %w", err)
	}

	update := bson.M{
		"$set": bson.M{
			"chat_id":       sg.ChatID,
			"host_id":       int64(sg.HostID),
			"host_username": sg.HostUsername,
			"host_name":     sg.HostName,
			"scheduled_at":  sg.ScheduledAt,
			"created_at":    sg.CreatedAt,
			"schedule_json": string(data),
		},
	}
	_, err = m.scheduled.UpdateOne(ctx,
		bson.M{"chat_id": sg.ChatID}, update, options.Update().SetUpsert(true))
	return err
}

func (m *MongoStore) GetScheduledGame(chatID int64) (*ScheduledGame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc struct {
		ScheduleJSON string `bson:"schedule_json"`
	}
	err := m.scheduled.FindOne(ctx, bson.M{"chat_id": chatID}).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("find scheduled game: %w", err)
	}

	var sg ScheduledGame
	if err := json.Unmarshal([]byte(doc.ScheduleJSON), &sg); err != nil {
		return nil, fmt.Errorf("unmarshal scheduled game: %w", err)
	}
	return &sg, nil
}

func (m *MongoStore) DeleteScheduledGame(chatID int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := m.scheduled.DeleteOne(ctx, bson.M{"chat_id": chatID})
	return err
}

func (m *MongoStore) ListDueScheduledGames(before time.Time) ([]*ScheduledGame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := m.scheduled.Find(ctx, bson.M{
		"scheduled_at": bson.M{"$lte": before},
	})
	if err != nil {
		return nil, fmt.Errorf("list due schedules: %w", err)
	}
	defer cursor.Close(ctx)

	var due []*ScheduledGame
	for cursor.Next(ctx) {
		var doc struct {
			ScheduleJSON string `bson:"schedule_json"`
		}
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode scheduled game: %w", err)
		}
		var sg ScheduledGame
		if err := json.Unmarshal([]byte(doc.ScheduleJSON), &sg); err != nil {
			return nil, fmt.Errorf("unmarshal scheduled game: %w", err)
		}
		due = append(due, &sg)
	}
	return due, cursor.Err()
}
