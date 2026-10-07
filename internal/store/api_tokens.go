package store

import "context"

// One token per user makes reissue an atomic replacement and revocation immediate.
func (s *Store) PutAPIToken(ctx context.Context, token APIToken) error {
	_, err := s.client.Collection("api_tokens").Doc(token.User.ID).Set(ctx, token)
	return err
}
func (s *Store) APIToken(ctx context.Context, uid string) (APIToken, error) {
	d, err := s.client.Collection("api_tokens").Doc(uid).Get(ctx)
	if err != nil {
		return APIToken{}, normalizeError(err)
	}
	var token APIToken
	err = d.DataTo(&token)
	return token, err
}
func (s *Store) DeleteAPIToken(ctx context.Context, uid string) error {
	_, err := s.client.Collection("api_tokens").Doc(uid).Delete(ctx)
	return err
}
