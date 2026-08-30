// Package mongo implements the MongoDB document-store backend of the
// hexagon, proving the port works across storage paradigms.
//
// Tasks are stored as BSON documents (taskDoc) mapped to/from the domain
// Task via toDoc/fromDoc; the database name is taken from the connection
// string's path. The provider is registered with the factory as TypeMongoDB.
//
// Public API:
//   - Types:  TaskRepositoryMongo
//   - Methods: the ten task methods and seven user methods
//
// Private:
//   - provider, disconnectCloser, dbName
//   - taskDoc, toDoc, fromDoc, ensureIndexes, ensureUserIndexes
package mongo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// Registration: importing this package makes the mongodb backend available to
// the repository factory.
func init() {
	repository.Register(repository.TypeMongoDB, provider{})
}

// provider implements repository.Provider for MongoDB. The URI is a
// mongodb:// connection string; the database name is taken from its path.
type provider struct{}

func (provider) Open(ctx context.Context, uri string) (repository.TaskRepository, io.Closer, error) {
	if strings.TrimSpace(uri) == "" {
		return nil, nil, fmt.Errorf("mongodb: HEXARCH_DB_URI is required")
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, nil, fmt.Errorf("mongodb: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("mongodb: ping: %w", err)
	}
	r := &TaskRepositoryMongo{client: client, coll: client.Database(dbName(uri)).Collection("tasks")}
	if err := r.ensureIndexes(ctx); err != nil {
		client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("mongodb: indexes: %w", err)
	}
	if err := r.ensureUserIndexes(ctx); err != nil {
		client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("mongodb: user indexes: %w", err)
	}
	if err := r.seedAdminMongo(ctx); err != nil {
		client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("mongodb: seed: %w", err)
	}
	return r, disconnectCloser{client}, nil
}

// dbName extracts the database name from a connection string, defaulting to
// "hexarch" when the URI has no path.
func dbName(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return "hexarch"
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" || strings.Contains(name, "?") || strings.Contains(name, "/") {
		return "hexarch"
	}
	return name
}

type disconnectCloser struct{ client *mongo.Client }

func (c disconnectCloser) Close() error { return c.client.Disconnect(context.Background()) }

// ---- repository ----

// TaskRepositoryMongo is a document-backed implementation of the
// TaskRepository port. The domain Task is mapped through a BSON-tagged DTO so
// the domain stays free of infrastructure concerns.
type TaskRepositoryMongo struct {
	client *mongo.Client
	coll   *mongo.Collection
}

// taskDoc is the durable representation of a task in MongoDB.
type taskDoc struct {
	ID          string     `bson:"_id"`
	UserID      string     `bson:"userid"`
	Title       string     `bson:"title"`
	Description string     `bson:"description"`
	Status      string     `bson:"status"`
	Priority    int        `bson:"priority"`
	Deadline    *time.Time `bson:"deadline,omitempty"`
	CreatedAt   time.Time  `bson:"created_at"`
	UpdatedAt   time.Time  `bson:"updated_at"`
}

// toDoc maps a domain Task onto its durable BSON representation.
func toDoc(t domain.Task) taskDoc {
	return taskDoc{
		ID:          t.ID().String(),
		UserID:      t.UserID().String(),
		Title:       t.Title(),
		Description: t.Description(),
		Status:      t.Status().String(),
		Priority:    t.Priority(),
		Deadline:    t.Deadline(),
		CreatedAt:   t.CreatedAt(),
		UpdatedAt:   t.UpdatedAt(),
	}
}

// fromDoc rebuilds a domain Task from a stored document, validating the
// stored values through the domain hydrator.
func fromDoc(doc taskDoc) (domain.Task, error) {
	t, err := domain.HydrateTask(
		domain.TaskID(doc.ID), domain.UserID(doc.UserID), doc.Title, doc.Description, domain.Status(doc.Status),
		doc.Priority, doc.Deadline, doc.CreatedAt, doc.UpdatedAt)
	if err != nil {
		return domain.Task{}, domain.Storage("corrupted document: " + err.Error())
	}
	return t, nil
}

func (r *TaskRepositoryMongo) ensureIndexes(ctx context.Context) error {
	// Mirror the port's ordering contract (priority desc, created_at desc)
	// and the common status filter.
	_, err := r.coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "priority", Value: -1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	})
	return err
}

// Create inserts a new task, mapping duplicate-key errors to
// domain.Conflict.
func (r *TaskRepositoryMongo) Create(ctx context.Context, t domain.Task) error {
	_, err := r.coll.InsertOne(ctx, toDoc(t))
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.Conflict("task " + t.ID().String() + " already exists")
		}
		return domain.Storage("insert failed: " + err.Error())
	}
	return nil
}

// ByID returns the task with the given _id, or domain.NotFound.
func (r *TaskRepositoryMongo) ByID(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	var doc taskDoc
	err := r.coll.FindOne(ctx, bson.M{"_id": id.String()}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Task{}, domain.NotFound("task " + id.String() + " not found")
		}
		return domain.Task{}, domain.Storage("query failed: " + err.Error())
	}
	return fromDoc(doc)
}

// List returns the tasks matching the filter with the same ordering and
// paging contract as the SQL backends: priority desc, then created_at desc.
func (r *TaskRepositoryMongo) List(ctx context.Context, f repository.TaskFilter) ([]domain.Task, error) {
	filter := bson.M{}
	if f.UserID != nil {
		filter["userid"] = f.UserID.String()
	}
	if f.Status != nil {
		filter["status"] = f.Status.String()
	}
	if f.Search != "" {
		re := primitive.Regex{Pattern: regexp.QuoteMeta(f.Search), Options: "i"}
		filter["$or"] = bson.A{
			bson.M{"title": re},
			bson.M{"description": re},
		}
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "priority", Value: -1}, {Key: "created_at", Value: -1}}).
		SetSkip(int64(offset)).
		SetLimit(int64(limit))

	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return []domain.Task{}, domain.Storage("list failed: " + err.Error())
	}
	defer cur.Close(ctx)

	var out []domain.Task
	for cur.Next(ctx) {
		var doc taskDoc
		if err := cur.Decode(&doc); err != nil {
			return out, domain.Storage("scan failed: " + err.Error())
		}
		t, err := fromDoc(doc)
		if err != nil {
			return out, err
		}
		out = append(out, t)
	}
	if err := cur.Err(); err != nil {
		return []domain.Task{}, domain.Storage(err.Error())
	}
	return out, nil
}

// Count returns the total number of documents in the collection, honoring
// the filter's UserID scope (nil = all users, admin scope).
func (r *TaskRepositoryMongo) Count(ctx context.Context, f repository.TaskFilter) (int, error) {
	filter := bson.M{}
	if f.UserID != nil {
		filter["userid"] = f.UserID.String()
	}
	n, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return 0, domain.Storage("count failed: " + err.Error())
	}
	return int(n), nil
}

// CountByStatus returns the number of documents in the given status, honoring
// the filter's UserID scope (nil = all users, admin scope).
func (r *TaskRepositoryMongo) CountByStatus(ctx context.Context, status domain.Status, f repository.TaskFilter) (int, error) {
	filter := bson.M{"status": status.String()}
	if f.UserID != nil {
		filter["userid"] = f.UserID.String()
	}
	n, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return 0, domain.Storage("count failed: " + err.Error())
	}
	return int(n), nil
}

// Update replaces the document for the task's ID; a non-matching ID yields
// domain.NotFound.
func (r *TaskRepositoryMongo) Update(ctx context.Context, t domain.Task) error {
	result, err := r.coll.ReplaceOne(ctx, bson.M{"_id": t.ID().String()}, toDoc(t))
	if err != nil {
		return domain.Storage("update failed: " + err.Error())
	}
	if result.MatchedCount == 0 {
		return domain.NotFound("task " + t.ID().String() + " not found")
	}
	return nil
}

// Delete removes the document for the given ID, returning domain.NotFound
// when it was already absent.
func (r *TaskRepositoryMongo) Delete(ctx context.Context, id domain.TaskID) error {
	result, err := r.coll.DeleteOne(ctx, bson.M{"_id": id.String()})
	if err != nil {
		return domain.Storage("delete failed: " + err.Error())
	}
	if result.DeletedCount == 0 {
		return domain.NotFound("task " + id.String() + " not found")
	}
	return nil
}
