package store

import (
	"cloud.google.com/go/firestore"
	"context"
	"time"
)

func (s *Store) PutSession(ctx context.Context, token string, session Session) error {
	batch := s.client.Batch()
	batch.Set(s.client.Collection("users").Doc(session.User.ID), session.User)
	batch.Set(s.client.Collection("sessions").Doc(Hash(token)), session)
	_, err := batch.Commit(ctx)
	return err
}
func (s *Store) Session(ctx context.Context, token string) (Session, error) {
	d, err := s.client.Collection("sessions").Doc(Hash(token)).Get(ctx)
	if err != nil {
		return Session{}, normalizeError(err)
	}
	var session Session
	if err = d.DataTo(&session); err != nil {
		return session, err
	}
	if !session.ExpiresAt.After(time.Now()) {
		return Session{}, ErrNotFound
	}
	return session, nil
}
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.client.Collection("sessions").Doc(Hash(token)).Delete(ctx)
	return err
}
func (s *Store) PutOAuth(ctx context.Context, state string, value OAuthState) error {
	_, err := s.client.Collection("oauth_states").Doc(Hash(state)).Set(ctx, value)
	return err
}
func (s *Store) ConsumeOAuth(ctx context.Context, state string) (OAuthState, error) {
	var value OAuthState
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ref := s.client.Collection("oauth_states").Doc(Hash(state))
		d, err := tx.Get(ref)
		if err != nil {
			return normalizeError(err)
		}
		if err = d.DataTo(&value); err != nil {
			return err
		}
		if !value.ExpiresAt.After(time.Now()) {
			return ErrNotFound
		}
		return tx.Delete(ref)
	})
	return value, err
}
