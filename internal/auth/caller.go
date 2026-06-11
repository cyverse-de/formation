package auth

import "context"

// Caller is the resolved identity for one request: the terrain-ready bearer
// token plus the backend username used in generated values (output_dir,
// analysis names). For service accounts the token is an impersonation token
// for the mapped username, so terrain sees a first-class user.
type Caller struct {
	Token    string
	Username string
}

// CallerResolver builds Callers from authenticated identities, exchanging
// service-account tokens for impersonated user tokens.
type CallerResolver struct {
	impersonator            *Impersonator
	serviceAccountUsernames map[string]string
}

// NewCallerResolver wires the resolver with the impersonator and the
// configured service-account-to-username mapping.
func NewCallerResolver(impersonator *Impersonator, serviceAccountUsernames map[string]string) *CallerResolver {
	return &CallerResolver{impersonator: impersonator, serviceAccountUsernames: serviceAccountUsernames}
}

// Resolve returns the Caller for an identity: users keep their own token,
// service accounts get an impersonation token for their mapped username.
func (r *CallerResolver) Resolve(ctx context.Context, info *Info) (*Caller, error) {
	username, err := info.UsernameForBackend(r.serviceAccountUsernames)
	if err != nil {
		return nil, err
	}

	token := info.Token
	if info.Type == TypeServiceAccount {
		token, err = r.impersonator.TokenFor(ctx, info.Token, username)
		if err != nil {
			return nil, err
		}
	}
	return &Caller{Token: token, Username: username}, nil
}

// UserCaller resolves the data-path identity: any valid token acts as a user
// under its JWT username, with no service-account mapping or impersonation.
func UserCaller(info *Info) (*Caller, error) {
	username, err := info.Claims.Username()
	if err != nil {
		return nil, err
	}
	return &Caller{Token: info.Token, Username: username}, nil
}
