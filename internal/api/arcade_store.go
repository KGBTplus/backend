package api

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	viewCenter    = 500.0
	viewAmplitude = 450.0
	hitThreshold  = 45.0
	maxTorpedoes  = 10
)

var (
	trajectoryX = [8]float64{180, 300, 420, 480, 520, 580, 700, 820}
	travelTime  = [8]float64{3.0, 2.5, 2.0, 1.7, 1.7, 2.0, 2.5, 3.0}
)

const (
	arcadeSessionTTL    = 30 * time.Minute
	arcadeCleanupPeriod = 5 * time.Minute
)

type ArcadeSession struct {
	ID            string     `json:"id"`
	Score         int        `json:"score"`
	TorpedoesLeft int        `json:"torpedoes_left"`
	ShipSpeed     float64    `json:"ship_speed"`
	ShipPhase     float64    `json:"ship_phase"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Status        string     `json:"status"`
}

type ArcadeStore struct {
	mu       sync.RWMutex
	sessions map[string]*ArcadeSession
}

func NewArcadeStore() *ArcadeStore {
	as := &ArcadeStore{
		sessions: make(map[string]*ArcadeSession),
	}
	go as.cleanupLoop()
	return as
}

func (as *ArcadeStore) cleanupLoop() {
	for {
		time.Sleep(arcadeCleanupPeriod)
		as.mu.Lock()
		now := time.Now()
		for id, sess := range as.sessions {
			if sess.Status == "finished" || now.Sub(sess.StartedAt) > arcadeSessionTTL {
				delete(as.sessions, id)
			}
		}
		as.mu.Unlock()
	}
}

func (as *ArcadeStore) Create() *ArcadeSession {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	sess := &ArcadeSession{
		ID:            uuid.New().String(),
		TorpedoesLeft: maxTorpedoes,
		ShipSpeed:     0.8 + rng.Float64()*0.8,
		ShipPhase:     rng.Float64() * 2 * math.Pi,
		StartedAt:     time.Now(),
		Status:        "playing",
	}
	as.mu.Lock()
	as.sessions[sess.ID] = sess
	as.mu.Unlock()
	return sess
}

func (as *ArcadeStore) Get(id string) (*ArcadeSession, bool) {
	as.mu.RLock()
	s, ok := as.sessions[id]
	as.mu.RUnlock()
	return s, ok
}

func calcShipX(speed, phase float64, elapsed float64) float64 {
	return viewCenter + viewAmplitude*math.Sin(speed*elapsed+phase)
}

func (as *ArcadeStore) Shoot(sessionID string, trajectory int) (sess *ArcadeSession, hit bool, errMsg string) {
	as.mu.Lock()
	defer as.mu.Unlock()

	sess, ok := as.sessions[sessionID]
	if !ok {
		return nil, false, "Сессия не найдена"
	}
	if sess.Status != "playing" {
		return nil, false, "Игра уже завершена"
	}
	if trajectory < 0 || trajectory >= 8 {
		return nil, false, "Неверная траектория (0-7)"
	}
	if sess.TorpedoesLeft <= 0 {
		return nil, false, "Торпеды кончились"
	}

	sess.TorpedoesLeft--
	elapsed := time.Since(sess.StartedAt).Seconds() + travelTime[trajectory]
	shipX := calcShipX(sess.ShipSpeed, sess.ShipPhase, elapsed)

	hit = math.Abs(shipX-trajectoryX[trajectory]) < hitThreshold
	if hit {
		sess.Score++
	}

	if sess.TorpedoesLeft <= 0 {
		sess.Status = "finished"
		now := time.Now()
		sess.FinishedAt = &now
	}

	return sess, hit, ""
}
