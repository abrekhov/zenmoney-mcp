package zenmoney

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Snapshot struct {
	ServerTimestamp int64
	Synced          bool
	Accounts        []Account
	Tags            []Tag
	Merchants       []Merchant
	Companies       []Company
	Instruments     []Instrument
	Transactions    []Transaction
	Users           []User
}

type State struct {
	api API

	opMu sync.Mutex
	mu   sync.RWMutex

	serverTimestamp int64
	synced          bool
	accounts        map[string]Account
	tags            map[string]Tag
	merchants       map[string]Merchant
	companies       map[int]Company
	instruments     map[int]Instrument
	transactions    map[string]Transaction
	users           map[int]User
}

func NewState(api API) *State {
	return &State{
		api:          api,
		accounts:     make(map[string]Account),
		tags:         make(map[string]Tag),
		merchants:    make(map[string]Merchant),
		companies:    make(map[int]Company),
		instruments:  make(map[int]Instrument),
		transactions: make(map[string]Transaction),
		users:        make(map[int]User),
	}
}

func (s *State) EnsureSynced(ctx context.Context) error {
	s.mu.RLock()
	synced := s.synced
	s.mu.RUnlock()
	if synced {
		return nil
	}
	_, err := s.Sync(ctx, false)
	return err
}

func (s *State) Sync(ctx context.Context, forceFull bool) (Snapshot, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	s.mu.RLock()
	timestamp := s.serverTimestamp
	s.mu.RUnlock()
	if forceFull {
		timestamp = 0
	}

	req := DiffRequest{
		CurrentClientTimestamp: time.Now().Unix(),
		ServerTimestamp:        timestamp,
	}
	if timestamp == 0 {
		req.ForceFetch = []string{
			"instrument", "company", "account", "tag", "merchant", "transaction", "user",
		}
	}

	resp, err := s.api.Diff(ctx, req)
	if err != nil {
		return Snapshot{}, err
	}

	s.mu.Lock()
	if forceFull {
		s.accounts = make(map[string]Account)
		s.tags = make(map[string]Tag)
		s.merchants = make(map[string]Merchant)
		s.companies = make(map[int]Company)
		s.instruments = make(map[int]Instrument)
		s.transactions = make(map[string]Transaction)
		s.users = make(map[int]User)
	}
	s.applyDiffLocked(resp)
	s.synced = true
	snapshot := s.snapshotLocked()
	s.mu.Unlock()
	return snapshot, nil
}

func (s *State) AddTransaction(ctx context.Context, transaction Transaction) (Snapshot, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	s.mu.RLock()
	if !s.synced {
		s.mu.RUnlock()
		return Snapshot{}, fmt.Errorf("ZenMoney data has not been synchronized")
	}
	timestamp := s.serverTimestamp
	s.mu.RUnlock()

	resp, err := s.api.Diff(ctx, DiffRequest{
		CurrentClientTimestamp: time.Now().Unix(),
		ServerTimestamp:        timestamp,
		Transaction:            []Transaction{transaction},
	})
	if err != nil {
		return Snapshot{}, err
	}

	s.mu.Lock()
	s.applyDiffLocked(resp)
	if !transaction.Deleted {
		s.transactions[transaction.ID] = transaction
	}
	snapshot := s.snapshotLocked()
	s.mu.Unlock()
	return snapshot, nil
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *State) applyDiffLocked(resp DiffResponse) {
	if resp.ServerTimestamp > 0 {
		s.serverTimestamp = resp.ServerTimestamp
	}
	for _, item := range resp.Instrument {
		s.instruments[item.ID] = item
	}
	for _, item := range resp.Company {
		s.companies[item.ID] = item
	}
	for _, item := range resp.User {
		s.users[item.ID] = item
	}
	for _, item := range resp.Account {
		s.accounts[item.ID] = item
	}
	for _, item := range resp.Tag {
		s.tags[item.ID] = item
	}
	for _, item := range resp.Merchant {
		s.merchants[item.ID] = item
	}
	for _, item := range resp.Transaction {
		if item.Deleted {
			delete(s.transactions, item.ID)
		} else {
			s.transactions[item.ID] = item
		}
	}
	for _, item := range resp.Deletion {
		switch item.Object {
		case "transaction":
			delete(s.transactions, item.ID)
		case "account":
			delete(s.accounts, item.ID)
		case "tag":
			delete(s.tags, item.ID)
		case "merchant":
			delete(s.merchants, item.ID)
		}
	}
}

func (s *State) snapshotLocked() Snapshot {
	out := Snapshot{ServerTimestamp: s.serverTimestamp, Synced: s.synced}
	for _, item := range s.accounts {
		out.Accounts = append(out.Accounts, item)
	}
	for _, item := range s.tags {
		out.Tags = append(out.Tags, item)
	}
	for _, item := range s.merchants {
		out.Merchants = append(out.Merchants, item)
	}
	for _, item := range s.companies {
		out.Companies = append(out.Companies, item)
	}
	for _, item := range s.instruments {
		out.Instruments = append(out.Instruments, item)
	}
	for _, item := range s.transactions {
		out.Transactions = append(out.Transactions, item)
	}
	for _, item := range s.users {
		out.Users = append(out.Users, item)
	}
	sort.Slice(out.Accounts, func(i, j int) bool {
		return strings.ToLower(out.Accounts[i].Title) < strings.ToLower(out.Accounts[j].Title)
	})
	sort.Slice(out.Tags, func(i, j int) bool { return strings.ToLower(out.Tags[i].Title) < strings.ToLower(out.Tags[j].Title) })
	sort.Slice(out.Merchants, func(i, j int) bool {
		return strings.ToLower(out.Merchants[i].Title) < strings.ToLower(out.Merchants[j].Title)
	})
	sort.Slice(out.Companies, func(i, j int) bool { return out.Companies[i].ID < out.Companies[j].ID })
	sort.Slice(out.Instruments, func(i, j int) bool { return out.Instruments[i].ID < out.Instruments[j].ID })
	sort.Slice(out.Transactions, func(i, j int) bool {
		if out.Transactions[i].Date == out.Transactions[j].Date {
			return out.Transactions[i].Created > out.Transactions[j].Created
		}
		return out.Transactions[i].Date > out.Transactions[j].Date
	})
	sort.Slice(out.Users, func(i, j int) bool { return out.Users[i].ID < out.Users[j].ID })
	return out
}
