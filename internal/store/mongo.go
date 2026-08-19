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
)

type MongoStore struct {
	client      *mongo.Client
	db          *mongo.Database
	games       *mongo.Collection
	waitlists   *mongo.Collection
	dmConfirmed *mongo.Collection
	cooldowns   *mongo.Collection
}

func NewMongoStore(uri, dbName string) (*MongoStore, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	db := client.Database(dbName)

	// Create indexes
	gamesCol := db.Collection("games")
	gamesCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "game_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	waitlistsCol := db.Collection("waitlists")
	waitlistsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "chat_id", Value: 1}},
	})

	dmCol := db.Collection("dm_confirmed")
	dmCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "player_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	cooldownsCol := db.Collection("cooldowns")
	cooldownsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "key", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	// TTL index: cooldowns expire after 30 seconds
	cooldownsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "created_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(30),
	})

	return &MongoStore{
		client:      client,
		db:          db,
		games:       gamesCol,
		waitlists:   waitlistsCol,
		dmConfirmed: dmCol,
		cooldowns:   cooldownsCol,
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
