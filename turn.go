package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

var now = time.Now

const credentialsURL = "https://api.subspace.com/v1/globalturn"
const tokenURL = "https://subspace.auth0.com/oauth/token"

// ICEServer is used to represent a STUN or TURN server
type ICEServer struct {
	// NOTE: in code it's URL, elsewhere it's urls as in w3c
	URL        string `redis:"urls" json:"urls"`
	Username   string `redis:"username" json:"username"`
	Credential string `redis:"credential" json:"credential"`
	Active     bool   `redis:"active" json:"-"`
}

// genCredential returns a username and credential for a TURN server
// based on the username and the secret key
func genCredential(username string) (string, string) {
	secretKey := os.Getenv("TURN_SECRET_KEY")
	if secretKey == "" {
		secretKey = "thisisatest"
	}
	h := hmac.New(sha1.New, []byte(secretKey))
	timestamp := now().Add(24 * time.Hour).Unix()
	compuser := fmt.Sprintf("%s:%d", username, timestamp)
	_, _ = h.Write([]byte(compuser))
	// return the compound username and the base64 encoded HMAC-SHA1
	return compuser, base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func serveICEServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Only the POST method is supported", http.StatusBadRequest)
		return
	}
	servers, err := db.GetICEServers()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read ICESevers from db: %s", err),
			http.StatusInternalServerError)
		return
	}
	if len(servers) == 0 {
		http.Error(w, "No ICE servers found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("["))
	for i, s := range servers {
		if s.URL[:5] == "turn:" {
			// generate turn credentials. get the username from the request body
			// and use the secret key to generate the credentials
			// here is an example of a valit post request:
			//
			// curl -X POST http://localhost:17777/iceservers?email=test%40example.com
			//
			username := r.FormValue("email")
			if username == "" {
				http.Error(w, "No username provided", http.StatusBadRequest)
				return
			}
			s.Username, s.Credential = genCredential(username)
		}
		b, err := json.Marshal(s)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to marshal ICE server: %s", err),
				http.StatusInternalServerError)
			return
		}
		if s.Active {
			w.Write(b)
			if i != len(servers)-1 {
				w.Write([]byte(","))
			}
		}
	}
	w.Write([]byte("]"))
}
