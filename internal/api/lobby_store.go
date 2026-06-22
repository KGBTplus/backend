package api

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MemoryLobby struct {
	ID         uuid.UUID
	CreatorID  uuid.UUID
	InviteCode string
	Status     string
	MaxPlayers int32
	Players    []uuid.UUID
	CreatedAt  time.Time
}

type MemoryLobbyStore struct {
	mu      sync.RWMutex
	lobbies map[uuid.UUID]*MemoryLobby
}

func NewMemoryLobbyStore() *MemoryLobbyStore {
	return &MemoryLobbyStore{
		lobbies: make(map[uuid.UUID]*MemoryLobby),
	}
}

func (ls *MemoryLobbyStore) Create(creatorID uuid.UUID) *MemoryLobby {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := &MemoryLobby{
		ID:         uuid.New(),
		CreatorID:  creatorID,
		InviteCode: genInviteCode(),
		Status:     "waiting",
		MaxPlayers: 2,
		Players:    []uuid.UUID{creatorID},
		CreatedAt:  time.Now(),
	}
	ls.lobbies[l.ID] = l
	return l
}

func (ls *MemoryLobbyStore) Get(id uuid.UUID) (*MemoryLobby, bool) {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	l, ok := ls.lobbies[id]
	return l, ok
}

func (ls *MemoryLobbyStore) List(status string) []*MemoryLobby {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	var result []*MemoryLobby
	for _, l := range ls.lobbies {
		if status == "" || l.Status == status {
			result = append(result, l)
		}
	}
	return result
}

func (ls *MemoryLobbyStore) Join(lobbyID, userID uuid.UUID) (*MemoryLobby, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l, ok := ls.lobbies[lobbyID]
	if !ok {
		return nil, fmt.Errorf("Лобби не найдено")
	}
	if l.Status != "waiting" {
		return nil, fmt.Errorf("Лобби уже заполнено")
	}
	for _, p := range l.Players {
		if p == userID {
			return l, nil
		}
	}
	l.Players = append(l.Players, userID)
	if len(l.Players) >= int(l.MaxPlayers) {
		l.Status = "full"
	}
	return l, nil
}

func (ls *MemoryLobbyStore) Leave(lobbyID, userID uuid.UUID) (*MemoryLobby, bool, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l, ok := ls.lobbies[lobbyID]
	if !ok {
		return nil, false, fmt.Errorf("Лобби не найдено")
	}
	found := false
	for i, p := range l.Players {
		if p == userID {
			l.Players = append(l.Players[:i], l.Players[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return nil, false, fmt.Errorf("Вы не в этом лобби")
	}
	isCreator := l.CreatorID == userID
	if isCreator || len(l.Players) == 0 {
		delete(ls.lobbies, lobbyID)
		return l, true, nil
	}
	l.Status = "waiting"
	return l, false, nil
}

func (ls *MemoryLobbyStore) FindByCode(code string) (*MemoryLobby, bool) {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	for _, l := range ls.lobbies {
		if l.InviteCode == code {
			return l, true
		}
	}
	return nil, false
}

func (ls *MemoryLobbyStore) Delete(id uuid.UUID) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	delete(ls.lobbies, id)
}

func (ls *MemoryLobbyStore) Cleanup() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	now := time.Now()
	for id, l := range ls.lobbies {
		if l.Status == "waiting" && now.Sub(l.CreatedAt) > 10*time.Minute {
			delete(ls.lobbies, id)
		}
	}
}

func (ls *MemoryLobbyStore) IsPlayerInLobby(userID uuid.UUID) (*MemoryLobby, bool) {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	for _, l := range ls.lobbies {
		for _, p := range l.Players {
			if p == userID {
				return l, true
			}
		}
	}
	return nil, false
}
