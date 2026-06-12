package auth

// Caller is the resolved identity for one request: the terrain-ready bearer
// token plus the username used in generated values (output_dir, analysis
// names).
type Caller struct {
	Token    string
	Username string
}

// UserCaller resolves a verified token into a Caller: the user acts under
// their JWT username and their own token is forwarded to terrain.
func UserCaller(claims *Claims, token string) (*Caller, error) {
	username, err := claims.Username()
	if err != nil {
		return nil, err
	}
	return &Caller{Token: token, Username: username}, nil
}
