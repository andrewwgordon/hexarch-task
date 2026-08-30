// user.go implements the user half of the TaskRepository port for the
// MongoDB backend: a users collection with unique email/apikey indexes, the
// seven user methods, owner-scoped task filtering, delete-user cascade, and
// the local bootstrap-admin seed (spec docs/auth.md §3.6).
//
// Public API: (none — methods on TaskRepositoryMongo)
//
// Private:
//   - userDoc, toUserDoc, fromUserDoc, usersCollection, ensureUserIndexes,
//     seedAdmin (replicates the sqldb seed policy)
package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"hexarch/internal/domain"
)

// userDoc is the durable representation of a user in MongoDB. Password holds
// only a bcrypt hash.
type userDoc struct {
	ID       string `bson:"_id"`
	Email    string `bson:"email"`
	Password string `bson:"password"`
	APIKey   string `bson:"apikey"`
	IsAdmin  bool   `bson:"isadmin"`
}

func toUserDoc(u domain.User) userDoc {
	return userDoc{
		ID:       u.ID().String(),
		Email:    u.Email(),
		Password: u.Password(),
		APIKey:   u.APIKey(),
		IsAdmin:  u.IsAdmin(),
	}
}

func fromUserDoc(d userDoc) (domain.User, error) {
	u, err := domain.HydrateUser(domain.UserID(d.ID), d.Email, d.Password, d.APIKey, d.IsAdmin)
	if err != nil {
		return domain.User{}, domain.Storage("corrupted user document: " + err.Error())
	}
	return u, nil
}

// usersCollection returns the users collection of the same database.
func (r *TaskRepositoryMongo) usersCollection() *mongo.Collection {
	return r.coll.Database().Collection("users")
}

// ensureUserIndexes creates the unique email and apikey indexes.
func (r *TaskRepositoryMongo) ensureUserIndexes(ctx context.Context) error {
	_, err := r.usersCollection().Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "apikey", Value: 1}}, Options: options.Index().SetUnique(true)},
	})
	return err
}

// seedAdminMongo inserts the bootstrap admin only when no users exist
// (replicates the sqldb seed policy; spec docs/auth.md §3.6, FR-U5).
func (r *TaskRepositoryMongo) seedAdminMongo(ctx context.Context) error {
	n, err := r.usersCollection().CountDocuments(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("seed: count users: %w", err)
	}
	if n > 0 {
		return nil
	}
	// bcrypt("admin") precomputed at MinCost for test/demo parity with the
	// memory and SQL seeds; the clear password never appears outside bcrypt.
	u, err := domain.NewUser(
		domain.UserID("00000000-0000-4000-8000-000000000002"),
		seedAdminEmailMongo,
		"$2a$04$X64jKbP6kJbubwOHQ3b4QO6OWQC.zl4FTLcZrdQn1O8DR9PCBGOdO",
		"0000000000000000000000000000000000000000000000000000000000000002",
		true,
	)
	if err != nil {
		return fmt.Errorf("seed: build admin: %w", err)
	}
	if _, err := r.usersCollection().InsertOne(ctx, toUserDoc(u)); err != nil {
		return fmt.Errorf("seed: insert admin: %w", err)
	}
	return nil
}

const seedAdminEmailMongo = "admin@email.com"

// CreateUser inserts a new user; duplicate email/apikey → domain.Conflict.
func (r *TaskRepositoryMongo) CreateUser(ctx context.Context, u domain.User) error {
	_, err := r.usersCollection().InsertOne(ctx, toUserDoc(u))
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.Conflict("user " + u.Email() + " already exists")
		}
		return domain.Storage("insert user failed: " + err.Error())
	}
	return nil
}

// UpdateUser replaces the user document; NotFound when absent, Conflict on
// duplicate email.
func (r *TaskRepositoryMongo) UpdateUser(ctx context.Context, u domain.User) error {
	result, err := r.usersCollection().ReplaceOne(ctx, bson.M{"_id": u.ID().String()}, toUserDoc(u))
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.Conflict("email " + u.Email() + " already exists")
		}
		return domain.Storage("update user failed: " + err.Error())
	}
	if result.MatchedCount == 0 {
		return domain.NotFound("user " + u.ID().String() + " not found")
	}
	return nil
}

// DeleteUser removes the user and (in sequence) all tasks owned by them.
// Multi-document transactions require a replica set, so the cascade is
// sequential; the contract is that after DeleteUser succeeds, no tasks with
// that owner remain (the user delete is executed last).
func (r *TaskRepositoryMongo) DeleteUser(ctx context.Context, id domain.UserID) error {
	users := r.usersCollection()
	var user userDoc
	err := users.FindOne(ctx, bson.M{"_id": id.String()}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.NotFound("user " + id.String() + " not found")
		}
		return domain.Storage("query failed: " + err.Error())
	}
	// Cascade: tasks first, then the user.
	if _, err := r.coll.DeleteMany(ctx, bson.M{"userid": id.String()}); err != nil {
		return domain.Storage("delete user tasks failed: " + err.Error())
	}
	if _, err := users.DeleteOne(ctx, bson.M{"_id": id.String()}); err != nil {
		return domain.Storage("delete user failed: " + err.Error())
	}
	return nil
}

// ListUsers returns every user ordered by email.
func (r *TaskRepositoryMongo) ListUsers(ctx context.Context) ([]domain.User, error) {
	cur, err := r.usersCollection().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "email", Value: 1}}))
	if err != nil {
		return nil, domain.Storage("list users failed: " + err.Error())
	}
	defer cur.Close(ctx)
	var out []domain.User
	for cur.Next(ctx) {
		var d userDoc
		if err := cur.Decode(&d); err != nil {
			return out, domain.Storage("scan failed: " + err.Error())
		}
		u, err := fromUserDoc(d)
		if err != nil {
			return out, err
		}
		out = append(out, u)
	}
	if err := cur.Err(); err != nil {
		return []domain.User{}, domain.Storage(err.Error())
	}
	return out, nil
}

// UserByID returns the user with the given _id, or domain.NotFound.
func (r *TaskRepositoryMongo) UserByID(ctx context.Context, id domain.UserID) (domain.User, error) {
	var d userDoc
	err := r.usersCollection().FindOne(ctx, bson.M{"_id": id.String()}).Decode(&d)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.User{}, domain.NotFound("user " + id.String() + " not found")
		}
		return domain.User{}, domain.Storage("query failed: " + err.Error())
	}
	return fromUserDoc(d)
}

// AuthUser is a LOOKUP ONLY by lower-cased email; no password verification.
func (r *TaskRepositoryMongo) AuthUser(ctx context.Context, email string) (domain.User, error) {
	var d userDoc
	err := r.usersCollection().FindOne(ctx, bson.M{"email": strings.ToLower(email)}).Decode(&d)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.User{}, domain.NotFound("user " + email + " not found")
		}
		return domain.User{}, domain.Storage("query failed: " + err.Error())
	}
	return fromUserDoc(d)
}

// UserByAPIKey looks a user up by API key, or domain.NotFound.
func (r *TaskRepositoryMongo) UserByAPIKey(ctx context.Context, key string) (domain.User, error) {
	var d userDoc
	err := r.usersCollection().FindOne(ctx, bson.M{"apikey": key}).Decode(&d)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.User{}, domain.NotFound("user by apikey not found")
		}
		return domain.User{}, domain.Storage("query failed: " + err.Error())
	}
	return fromUserDoc(d)
}
