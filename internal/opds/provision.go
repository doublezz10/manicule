package opds

// Zero-typing device provisioning (§4.4): generate /.crosspoint/opds.json for
// a one-time SD-card drop. The CrossPoint store loader accepts legacy
// plaintext passwords and re-obfuscates them on save — VERIFY this behavior
// on the physical X3 at build time; adjust shape/keying here if needed.

import (
	"encoding/json"
	"fmt"
)

// CrosspointServer mirrors firmware's OpdsServer{name, url, username, password}.
type CrosspointServer struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ProvisionJSON builds the file content: a single saved server pointing at
// this app's OPDS endpoint with the tiny credentials.
func ProvisionJSON(lanURL, username, pin string) ([]byte, error) {
	if lanURL == "" {
		return nil, fmt.Errorf("opds: no LAN URL available")
	}
	servers := []CrosspointServer{{
		Name:     "manicule",
		URL:      lanURL,
		Username: username,
		Password: pin,
	}}
	data, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
