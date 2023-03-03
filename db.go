package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gomodule/redigo/redis"
)

const TokenLen = 30      // in Bytes, four times that in base64 and urls
const TokenTTL = 300     // in Seconds
const EmailInterval = 60 // in Seconds
const MaxPeersPerUser = 10
const UserIDLength = 10

// DBType is the type that holds our db
type DBType struct {
	pool  *redis.Pool
	poolM sync.Mutex
}

// DBUser is the info we store about a user - a list of peers' fingerprint
type DBUser []string

// for testing we use a redis "double"
var redisDouble *miniredis.Miniredis

// RandomString generates a random string of n bytes
func RandomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// CreateToken creates a short-live token to be emailed to the user
func (d *DBType) CreateToken(email string) (string, error) {
	if email == "" {
		return "", fmt.Errorf("Failied to create a token for an empty email")
	}
	token := RandomString(TokenLen)
	key := fmt.Sprintf("token:%s", token)
	conn := d.getConn()
	defer conn.Close()
	_, err := conn.Do("SETEX", key, TokenTTL, email)
	if err != nil {
		return "", fmt.Errorf("Failed to set token: %w", err)
	}
	return token, nil
}
func (d *DBType) Connect(host string) error {
	// should we use mock redis?
	if redisDouble != nil {
		host = redisDouble.Addr()
	}
	d.poolM.Lock()
	d.pool = &redis.Pool{
		MaxIdle:     5,
		IdleTimeout: 5 * time.Second,
		Dial:        func() (redis.Conn, error) { return redis.Dial("tcp", host) },
	}
	d.poolM.Unlock()
	if redisDouble == nil {
		conn := d.getConn()
		defer conn.Close()
		_, err := conn.Do("GET", "SFJAWERWEQRQWER")
		if err != redis.ErrNil {
			return err
		}
	}
	return nil
}

// GetToken reads the value of a token, usually an email address
func (d *DBType) GetToken(token string) (string, error) {
	key := fmt.Sprintf("token:%s", token)
	conn := d.getConn()
	defer conn.Close()
	value, err := redis.String(conn.Do("GET", key))
	if err != nil {
		return "", fmt.Errorf("Failed to read token: %w:", err)
	}
	return value, nil
}

// GetUser gets a user from redis
func (d *DBType) GetUser(email string) (*DBUser, error) {
	var r DBUser
	key := fmt.Sprintf("user:%s", email)
	conn := d.getConn()
	defer conn.Close()
	values, err := redis.Values(conn.Do("SMEMBERS", key))
	if err != nil {
		return nil, fmt.Errorf("Failed to read user %q list: %w", email, err)
	}
	for _, fp := range values {
		r = append(r, string(fp.([]byte)))
	}
	return &r, nil
}
func (d *DBType) getDoc(key string, target interface{}) error {
	conn := d.getConn()
	defer conn.Close()
	values, err := redis.Values(conn.Do("HGETALL", key))
	if err = redis.ScanStruct(values, target); err != nil {
		return fmt.Errorf("Failed to scan peer %q: %w", key, err)
	}
	return nil
}
func (d *DBType) PeerExists(fp string) (bool, error) {
	key := fmt.Sprintf("peer:%s", fp)
	conn := d.getConn()
	defer conn.Close()
	return redis.Bool(conn.Do("EXISTS", key))
}
func (d *DBType) Close() error {
	return nil
	// return d.conn.Close()
}

// AddPeer adds or updates a peer
func (d *DBType) AddPeer(peer *Peer) error {
	conn := d.getConn()
	defer conn.Close()
	if peer.User != "" {
		key := fmt.Sprintf("user:%s", peer.User)
		values, err := redis.Values(conn.Do("SMEMBERS", key))
		if err != nil {
			return fmt.Errorf("Failed to read user %q list: %w", peer.User, err)
		}
		if len(values) == MaxPeersPerUser {
			return fmt.Errorf("User has too many peers")
		}
		_, err = conn.Do("SADD", key, peer.FP)
		if err != nil {
			// get the key's kind
			kind, err := redis.String(conn.Do("TYPE", key))
			return fmt.Errorf("Failed to add peer - kind %q: %w", kind, err)
		}
	}
	_, err := conn.Do("HSET", redis.Args{}.Add(peer.Key()).AddFlat(peer)...)
	if err != nil {
		return fmt.Errorf("Failed to add peer %q: %w", peer.FP, err)
	}
	return nil
}

// IsVerfied tests the db to see if a peer is verfied
func IsVerified(fp string) (bool, error) {
	key := fmt.Sprintf("peer:%s", fp)
	conn := db.pool.Get()
	defer conn.Close()
	verified, err := redis.Bool(conn.Do("HGET", key, "verified"))
	if err != nil {
		Logger.Warnf("Failed to get 'verified' field for %s", key)
		return false, err
	}
	return verified, err
}

// GetPeer gets a peer, using the hub as cache for connected peers
func GetPeer(fp string) (*Peer, error) {
	key := fmt.Sprintf("peer:%s", fp)
	var pd Peer
	err := db.getDoc(key, &pd)
	if err != nil {
		return nil, err
	}
	return &pd, nil
}

// VerifyPeer is a function that sets the peers verification and publishes
// it's new state to the user's channel
func VerifyPeer(fp string, verified bool) error {
	rc := db.pool.Get()
	defer rc.Close()
	key := fmt.Sprintf("peer:%s", fp)
	online, err := redis.Bool(rc.Do("HGET", key, "online"))
	if err != nil {
		online = false
	}
	if verified {
		rc.Do("HSET", key, "verified", "1")
		if online {
			SendMessage(fp, StatusMessage{200, "peer is verified"})
			Logger.Infof("Sent a 200 to %q - a newly verified peer", fp)
			user, err := redis.String(rc.Do("HGET", key, "user"))
			if err != nil {
				return fmt.Errorf("Failed to get a peer's user: %w", err)
			}
			// send the peers
			ps, err := GetUsersPeers(user)
			if err != nil {
				return err
			}
			return SendMessage(fp, map[string]interface{}{"peers": ps})
		}
	} else {
		rc.Do("HSET", key, "verified", "0")
		if online {
			SendMessage(fp, StatusMessage{http.StatusUnauthorized,
				"peer's verification was revoked"})
		}
	}
	user, err := redis.String(rc.Do("HGET", key, "user"))
	if err != nil {
		return fmt.Errorf("Failed to hget user from %s: %w", key, err)
	}
	// publish the peer's state
	return SendPeerUpdate(rc, user, fp, verified, online)
}
func (d *DBType) canSendEmail(email string) bool {
	key := fmt.Sprintf("dontsend:%s", email)
	conn := d.getConn()
	defer conn.Close()
	blocked, err := redis.Bool(conn.Do("EXISTS", key))
	if err != nil {
		Logger.Warnf("failed to check if key %q exists", key)
		return false
	}
	if blocked {
		return false
	}
	_, err = conn.Do("SETEX", key, EmailInterval, "1")
	return true
}
func (d *DBType) SetQRVerified(email string) error {
	key := fmt.Sprintf("QRVerified:%s", email)
	conn := d.getConn()
	defer conn.Close()
	_, err := conn.Do("SET", key, "1")
	return err
}
func (d *DBType) getConn() redis.Conn {
	d.poolM.Lock()
	conn := d.pool.Get()
	d.poolM.Unlock()
	return conn
}
func (d *DBType) IsQRVerified(email string) bool {
	key := fmt.Sprintf("QRVerified:%s", email)
	conn := d.getConn()
	defer conn.Close()
	seen, err := redis.Bool(conn.Do("EXISTS", key))
	if err != nil {
		Logger.Warnf("failed to check if key %q exists", key)
		return false
	}
	if seen {
		return true
	}
	return false
}
func (d *DBType) Reset() {
	conn := d.getConn()
	i := 0
	for {
		arr, err := redis.Values(conn.Do("SCAN", i, "MATCH", "peer:*"))
		if err != nil {
			Logger.Errorf("Failed to retrieve redis keys: %s", err)
		}
		i, err = redis.Int(arr[0], nil)
		keys, err := redis.Strings(arr[1], nil)
		for _, k := range keys {
			if _, err := conn.Do("HSET", k, "online", false); err != nil {
				Logger.Errorf("Failed to set %s online to false: %s", k, err)
			}
		}
		if i == 0 {
			break
		}
	}
}
func (d *DBType) GetICEServers() ([]ICEServer, error) {
	var ret []ICEServer
	conn := d.getConn()
	keys, err := redis.Strings(conn.Do("KEYS", "iceserver:*"))
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve iceserver:* keys: %s", err)
	}
	for _, key := range keys {
		var info ICEServer
		d.getDoc(key, &info)
		if !info.Active {
			continue
		}
		ret = append(ret, info)
	}
	return ret, nil
}

// DeletePeer deletes a peer from the database
func (d *DBType) DeletePeer(fp string) error {
	key := fmt.Sprintf("peer:%s", fp)
	conn := d.pool.Get()
	defer conn.Close()
	_, err := conn.Do("DEL", key)
	return err
}

// tempIDExists checks if a temporary ID exists
// it returns true if it does, false otherwise
// and an error if something went wrong
func (d *DBType) tempIDExists(id string) (bool, error) {
	key := fmt.Sprintf("tempid:%s", id)
	conn := d.pool.Get()
	defer conn.Close()
	exists, err := redis.Bool(conn.Do("EXISTS", key))
	if err != nil {
		return false, err
	}
	return exists, nil
}

// AddUser is used to add a new user to the data base
// it recieves an email. It returns a permanent user id.
// If the user already exists, it returns the permanent user id
// and an error
func (d *DBType) AddUser(email string) (string, error) {
	conn := d.pool.Get()
	defer conn.Close()
	key := fmt.Sprintf("id:%s", email)
	userID, err := redis.String(conn.Do("GET", key))
	if err != nil && err != redis.ErrNil {
		return "", fmt.Errorf("Failed to get %s: %w", key, err)
	}
	if userID != "" {
		return userID, fmt.Errorf("User already exists")
	}
	id := RandomString(UserIDLength)
	_, err = conn.Do("SET", key, id)
	if err != nil {
		return "", fmt.Errorf("Failed to set %s: %w", key, err)
	}
	key = fmt.Sprintf("u:%s", id)
	_, err = conn.Do("HSET", key, "email", email, "active", "1")
	if err != nil {
		return "", fmt.Errorf("Failed to set %s: %w", key, err)
	}
	return id, nil
}

func (d *DBType) RemoveTempID(tempID string) error {
	conn := d.pool.Get()
	defer conn.Close()
	key := fmt.Sprintf("tempid:%s", tempID)
	_, err := conn.Do("DEL", key)
	return err
}
func (d *DBType) AddTempID(tempID string) error {
	conn := d.pool.Get()
	defer conn.Close()
	key := fmt.Sprintf("tempid:%s", tempID)
	_, err := conn.Do("Set", key, "1")
	return err
	//  calculate how long the tempid should be valid for
	// ttl := expireAt/1000 - time.Now().Unix()
	// err = db.Expire(key, ttl)
}
func (d *DBType) SetUserActive(UserID string, active bool) error {
	conn := d.pool.Get()
	defer conn.Close()
	key := fmt.Sprintf("u:%s", UserID)
	_, err := conn.Do("HSET", key, "active", active)
	return err
}
