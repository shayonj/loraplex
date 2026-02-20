package discovery

import "context"

type Static struct {
	Base
}

func NewStatic(peers []string) *Static {
	s := &Static{}
	s.peers = peers
	return s
}

func (s *Static) Start(_ context.Context) error {
	s.SetPeers(s.peers)
	return nil
}

func (s *Static) Stop() {}
