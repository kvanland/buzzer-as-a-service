package buzzer

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxActiveGroups        = 500
	maxPlayersPerGroup     = 64
	maxSubscribersPerGroup = 128
	maxTotalSubscribers    = 1000
)

var (
	ErrNotFound     = errors.New("group not found")
	ErrUnauthorized = errors.New("not authorized")
	ErrLocked       = errors.New("buzzer is locked")
	ErrExpired      = errors.New("group expired")
	ErrInvalid      = errors.New("invalid request")
	ErrLimit        = errors.New("limit reached")
)

type Store struct {
	mu          sync.Mutex
	path        string
	ttl         time.Duration
	now         func() time.Time
	groups      map[string]*Group
	subscribers map[string]map[chan Snapshot]struct{}
}

type Group struct {
	Code         string             `json:"code"`
	CreatedAt    time.Time          `json:"createdAt"`
	LastActivity time.Time          `json:"lastActivity"`
	ExpiresAt    time.Time          `json:"expiresAt"`
	Host         HostSession        `json:"host"`
	Players      map[string]*Player `json:"players"`
	LockedAll    bool               `json:"lockedAll"`
	FirstBuzz    *Buzz              `json:"firstBuzz,omitempty"`
	Buzzes       []Buzz             `json:"buzzes"`
	Round        int                `json:"round"`
}

type HostSession struct {
	Name     string `json:"name"`
	Token    string `json:"token"`
	PlayerID string `json:"playerId"`
}

type Player struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Color    string    `json:"color"`
	Token    string    `json:"token"`
	Locked   bool      `json:"locked"`
	JoinedAt time.Time `json:"joinedAt"`
	LastSeen time.Time `json:"lastSeen"`
}

type Buzz struct {
	PlayerID   string    `json:"playerId"`
	PlayerName string    `json:"playerName"`
	Color      string    `json:"color"`
	At         time.Time `json:"at"`
	Order      int       `json:"order"`
}

type Snapshot struct {
	Code         string           `json:"code"`
	ExpiresAt    time.Time        `json:"expiresAt"`
	HostPlayerID string           `json:"hostPlayerId"`
	LockedAll    bool             `json:"lockedAll"`
	FirstBuzz    *Buzz            `json:"firstBuzz,omitempty"`
	Buzzes       []Buzz           `json:"buzzes"`
	Round        int              `json:"round"`
	Players      []PlayerSnapshot `json:"players"`
}

type PlayerSnapshot struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Locked   bool   `json:"locked"`
	IsHost   bool   `json:"isHost"`
	LastSeen string `json:"lastSeen"`
}

type CreateResult struct {
	Snapshot    Snapshot `json:"snapshot"`
	Code        string   `json:"code"`
	HostToken   string   `json:"hostToken"`
	PlayerID    string   `json:"playerId"`
	PlayerToken string   `json:"playerToken"`
}

type JoinResult struct {
	Snapshot    Snapshot `json:"snapshot"`
	PlayerID    string   `json:"playerId"`
	PlayerToken string   `json:"playerToken"`
}

type BuzzResult struct {
	Snapshot Snapshot `json:"snapshot"`
	Accepted bool     `json:"accepted"`
	Reason   string   `json:"reason,omitempty"`
}

func NewStore(path string, ttl time.Duration) (*Store, error) {
	store := &Store{
		path:        path,
		ttl:         ttl,
		now:         time.Now,
		groups:      make(map[string]*Group),
		subscribers: make(map[string]map[chan Snapshot]struct{}),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	store.SweepExpired()
	return store, nil
}

func NewMemoryStore(ttl time.Duration) *Store {
	return &Store{
		ttl:         ttl,
		now:         time.Now,
		groups:      make(map[string]*Group),
		subscribers: make(map[string]map[chan Snapshot]struct{}),
	}
}

func (s *Store) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = clock
}

func (s *Store) CreateGroup(hostName, color string) (CreateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	removed := s.sweepExpiredLocked(now)
	if len(s.groups) >= maxActiveGroups {
		if removed > 0 {
			_ = s.saveLocked()
		}
		return CreateResult{}, ErrLimit
	}
	code, err := s.uniqueCodeLocked()
	if err != nil {
		return CreateResult{}, err
	}
	player := &Player{
		ID:       token(8),
		Name:     cleanName(hostName, "Host"),
		Color:    cleanColor(color, "#ff4d6d"),
		Token:    token(18),
		JoinedAt: now,
		LastSeen: now,
	}
	group := &Group{
		Code:         code,
		CreatedAt:    now,
		LastActivity: now,
		ExpiresAt:    now.Add(s.ttl),
		Host: HostSession{
			Name:     player.Name,
			Token:    token(24),
			PlayerID: player.ID,
		},
		Players:   map[string]*Player{player.ID: player},
		LockedAll: false,
		Buzzes:    []Buzz{},
		Round:     1,
	}
	s.groups[code] = group
	if err := s.saveLocked(); err != nil {
		return CreateResult{}, err
	}
	s.notifyLocked(code)

	return CreateResult{
		Snapshot:    snapshotOf(group, now),
		Code:        code,
		HostToken:   group.Host.Token,
		PlayerID:    player.ID,
		PlayerToken: player.Token,
	}, nil
}

func (s *Store) JoinGroup(code, name, color, existingPlayerID, existingToken string) (JoinResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return JoinResult{}, err
	}
	now := s.touchLocked(group)

	if existingPlayerID != "" && existingToken != "" {
		if player, ok := group.Players[existingPlayerID]; ok && player.Token == existingToken {
			updatePlayerLocked(group, player, name, color)
			player.LastSeen = now
			if err := s.saveLocked(); err != nil {
				return JoinResult{}, err
			}
			s.notifyLocked(group.Code)
			return JoinResult{Snapshot: snapshotOf(group, now), PlayerID: player.ID, PlayerToken: player.Token}, nil
		}
	}

	if len(group.Players) >= maxPlayersPerGroup {
		return JoinResult{}, ErrLimit
	}

	player := &Player{
		ID:       token(8),
		Name:     cleanName(name, "Contestant"),
		Color:    cleanColor(color, "#2ec4b6"),
		Token:    token(18),
		JoinedAt: now,
		LastSeen: now,
	}
	group.Players[player.ID] = player
	if err := s.saveLocked(); err != nil {
		return JoinResult{}, err
	}
	s.notifyLocked(group.Code)
	return JoinResult{Snapshot: snapshotOf(group, now), PlayerID: player.ID, PlayerToken: player.Token}, nil
}

func (s *Store) HostReconnect(code, hostToken string) (CreateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return CreateResult{}, err
	}
	if group.Host.Token != hostToken {
		return CreateResult{}, ErrUnauthorized
	}
	now := s.touchLocked(group)
	player := group.Players[group.Host.PlayerID]
	player.LastSeen = now
	if err := s.saveLocked(); err != nil {
		return CreateResult{}, err
	}
	s.notifyLocked(group.Code)
	return CreateResult{
		Snapshot:    snapshotOf(group, now),
		Code:        group.Code,
		HostToken:   group.Host.Token,
		PlayerID:    player.ID,
		PlayerToken: player.Token,
	}, nil
}

func (s *Store) PlayerReconnect(code, playerID, playerToken string) (JoinResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return JoinResult{}, err
	}
	player, err := authorizePlayer(group, playerID, playerToken)
	if err != nil {
		return JoinResult{}, err
	}
	now := s.touchLocked(group)
	player.LastSeen = now
	if err := s.saveLocked(); err != nil {
		return JoinResult{}, err
	}
	s.notifyLocked(group.Code)
	return JoinResult{Snapshot: snapshotOf(group, now), PlayerID: player.ID, PlayerToken: player.Token}, nil
}

func (s *Store) UpdatePlayer(code, playerID, playerToken, name, color string) (JoinResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return JoinResult{}, err
	}
	player, err := authorizePlayer(group, playerID, playerToken)
	if err != nil {
		return JoinResult{}, err
	}
	now := s.touchLocked(group)
	updatePlayerLocked(group, player, name, color)
	player.LastSeen = now
	if err := s.saveLocked(); err != nil {
		return JoinResult{}, err
	}
	s.notifyLocked(group.Code)
	return JoinResult{Snapshot: snapshotOf(group, now), PlayerID: player.ID, PlayerToken: player.Token}, nil
}

func (s *Store) Buzz(code, playerID, playerToken string) (BuzzResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return BuzzResult{}, err
	}
	player, err := authorizePlayer(group, playerID, playerToken)
	if err != nil {
		return BuzzResult{}, err
	}
	now := s.touchLocked(group)
	player.LastSeen = now

	if group.LockedAll || player.Locked {
		return BuzzResult{Snapshot: snapshotOf(group, now), Accepted: false, Reason: ErrLocked.Error()}, nil
	}
	for _, existing := range group.Buzzes {
		if existing.PlayerID == player.ID {
			return BuzzResult{Snapshot: snapshotOf(group, now), Accepted: false, Reason: "already buzzed this round"}, nil
		}
	}

	buzz := Buzz{
		PlayerID:   player.ID,
		PlayerName: player.Name,
		Color:      player.Color,
		At:         now,
		Order:      len(group.Buzzes) + 1,
	}
	if group.FirstBuzz == nil {
		group.FirstBuzz = &buzz
	}
	group.Buzzes = append(group.Buzzes, buzz)
	if err := s.saveLocked(); err != nil {
		return BuzzResult{}, err
	}
	s.notifyLocked(group.Code)
	return BuzzResult{Snapshot: snapshotOf(group, now), Accepted: true}, nil
}

func (s *Store) Reset(code, hostToken string) (Snapshot, error) {
	return s.hostAction(code, hostToken, func(group *Group) {
		group.FirstBuzz = nil
		group.Buzzes = []Buzz{}
		group.Round++
	})
}

func (s *Store) ResetRoundCount(code, hostToken string) (Snapshot, error) {
	return s.hostAction(code, hostToken, func(group *Group) {
		group.FirstBuzz = nil
		group.Buzzes = []Buzz{}
		group.Round = 1
	})
}

func (s *Store) SetLockAll(code, hostToken string, locked bool) (Snapshot, error) {
	return s.hostAction(code, hostToken, func(group *Group) {
		group.LockedAll = locked
		for _, player := range group.Players {
			player.Locked = locked
		}
	})
}

func (s *Store) SetPlayerLock(code, hostToken, playerID string, locked bool) (Snapshot, error) {
	return s.hostAction(code, hostToken, func(group *Group) {
		if player := group.Players[playerID]; player != nil {
			player.Locked = locked
		}
	})
}

func (s *Store) RemovePlayer(code, hostToken, playerID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return Snapshot{}, err
	}
	if group.Host.Token != hostToken {
		return Snapshot{}, ErrUnauthorized
	}
	if playerID == group.Host.PlayerID {
		return Snapshot{}, ErrInvalid
	}
	if group.Players[playerID] == nil {
		return Snapshot{}, ErrNotFound
	}
	now := s.touchLocked(group)
	delete(group.Players, playerID)
	removeBuzzesForPlayerLocked(group, playerID)
	if err := s.saveLocked(); err != nil {
		return Snapshot{}, err
	}
	s.notifyLocked(group.Code)
	return snapshotOf(group, now), nil
}

func (s *Store) Snapshot(code string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotOf(group, s.now()), nil
}

func (s *Store) Subscribe(code string) (<-chan Snapshot, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return nil, nil, err
	}
	if len(s.subscribers[code]) >= maxSubscribersPerGroup || s.totalSubscribersLocked() >= maxTotalSubscribers {
		return nil, nil, ErrLimit
	}

	ch := make(chan Snapshot, 8)
	if s.subscribers[code] == nil {
		s.subscribers[code] = make(map[chan Snapshot]struct{})
	}
	s.subscribers[code][ch] = struct{}{}
	ch <- snapshotOf(group, s.now())

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subscribers[code], ch)
		close(ch)
	}
	return ch, cancel, nil
}

func (s *Store) totalSubscribersLocked() int {
	total := 0
	for _, subscribers := range s.subscribers {
		total += len(subscribers)
	}
	return total
}

func (s *Store) SweepExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := s.sweepExpiredLocked(s.now())
	if removed > 0 {
		_ = s.saveLocked()
	}
	return removed
}

func (s *Store) sweepExpiredLocked(now time.Time) int {
	removed := 0
	for code, group := range s.groups {
		if !group.ExpiresAt.After(now) {
			delete(s.groups, code)
			removed++
			s.notifyLocked(code)
		}
	}
	return removed
}

func (s *Store) RunJanitor(every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for range ticker.C {
		s.SweepExpired()
	}
}

func (s *Store) hostAction(code, hostToken string, fn func(*Group)) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.groupLocked(code)
	if err != nil {
		return Snapshot{}, err
	}
	if group.Host.Token != hostToken {
		return Snapshot{}, ErrUnauthorized
	}
	now := s.touchLocked(group)
	fn(group)
	if err := s.saveLocked(); err != nil {
		return Snapshot{}, err
	}
	s.notifyLocked(group.Code)
	return snapshotOf(group, now), nil
}

func (s *Store) groupLocked(code string) (*Group, error) {
	group := s.groups[normalizeCode(code)]
	if group == nil {
		return nil, ErrNotFound
	}
	if !group.ExpiresAt.After(s.now()) {
		delete(s.groups, group.Code)
		_ = s.saveLocked()
		s.notifyLocked(group.Code)
		return nil, ErrExpired
	}
	return group, nil
}

func (s *Store) touchLocked(group *Group) time.Time {
	now := s.now()
	group.LastActivity = now
	group.ExpiresAt = now.Add(s.ttl)
	return now
}

func (s *Store) notifyLocked(code string) {
	group := s.groups[code]
	var snap Snapshot
	if group != nil {
		snap = snapshotOf(group, s.now())
	}
	for ch := range s.subscribers[code] {
		select {
		case ch <- snap:
		default:
		}
	}
}

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var groups map[string]*Group
	if err := json.Unmarshal(data, &groups); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	if groups != nil {
		s.groups = groups
	}
	for code, group := range s.groups {
		group.Code = normalizeCode(code)
		if group.Players == nil {
			group.Players = make(map[string]*Player)
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.groups, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) uniqueCodeLocked() (string, error) {
	for i := 0; i < 100; i++ {
		code, err := randomCode(5)
		if err != nil {
			return "", err
		}
		if s.groups[code] == nil {
			return code, nil
		}
	}
	return "", errors.New("could not allocate group code")
}

func updatePlayerLocked(group *Group, player *Player, name, color string) {
	player.Name = cleanName(name, player.Name)
	player.Color = cleanColor(color, player.Color)
	if group.Host.PlayerID == player.ID {
		group.Host.Name = player.Name
	}
	for i := range group.Buzzes {
		if group.Buzzes[i].PlayerID == player.ID {
			group.Buzzes[i].PlayerName = player.Name
			group.Buzzes[i].Color = player.Color
		}
	}
	if group.FirstBuzz != nil && group.FirstBuzz.PlayerID == player.ID {
		group.FirstBuzz.PlayerName = player.Name
		group.FirstBuzz.Color = player.Color
	}
}

func removeBuzzesForPlayerLocked(group *Group, playerID string) {
	kept := make([]Buzz, 0, len(group.Buzzes))
	for _, buzz := range group.Buzzes {
		if buzz.PlayerID != playerID {
			buzz.Order = len(kept) + 1
			kept = append(kept, buzz)
		}
	}
	group.Buzzes = kept
	if len(group.Buzzes) == 0 {
		group.FirstBuzz = nil
		return
	}
	first := group.Buzzes[0]
	group.FirstBuzz = &first
}

func authorizePlayer(group *Group, playerID, tokenValue string) (*Player, error) {
	player := group.Players[playerID]
	if player == nil || player.Token != tokenValue {
		return nil, ErrUnauthorized
	}
	return player, nil
}

func snapshotOf(group *Group, now time.Time) Snapshot {
	players := make([]PlayerSnapshot, 0, len(group.Players))
	for _, player := range group.Players {
		players = append(players, PlayerSnapshot{
			ID:       player.ID,
			Name:     player.Name,
			Color:    player.Color,
			Locked:   player.Locked,
			IsHost:   player.ID == group.Host.PlayerID,
			LastSeen: relativeTime(now, player.LastSeen),
		})
	}
	sort.Slice(players, func(i, j int) bool {
		if players[i].IsHost != players[j].IsHost {
			return players[i].IsHost
		}
		return strings.ToLower(players[i].Name) < strings.ToLower(players[j].Name)
	})
	buzzes := make([]Buzz, len(group.Buzzes))
	copy(buzzes, group.Buzzes)
	var firstBuzz *Buzz
	if group.FirstBuzz != nil {
		first := *group.FirstBuzz
		firstBuzz = &first
	}
	return Snapshot{
		Code:         group.Code,
		ExpiresAt:    group.ExpiresAt,
		HostPlayerID: group.Host.PlayerID,
		LockedAll:    group.LockedAll,
		FirstBuzz:    firstBuzz,
		Buzzes:       buzzes,
		Round:        group.Round,
		Players:      players,
	}
}

func cleanName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback
	}
	if len(name) > 32 {
		return name[:32]
	}
	return name
}

func cleanColor(color, fallback string) string {
	color = strings.TrimSpace(color)
	if len(color) != 7 || color[0] != '#' {
		return fallback
	}
	for _, r := range color[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return fallback
		}
	}
	return color
}

func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func randomCode(length int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}

func token(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func relativeTime(now, then time.Time) string {
	if then.IsZero() {
		return "unknown"
	}
	d := now.Sub(then)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}
