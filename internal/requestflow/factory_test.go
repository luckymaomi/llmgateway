package requestflow

import (
	"sync"
	"testing"

	"github.com/luckymaomi/llmgateway/internal/security"
)

func TestProviderFactorySharesOneSSRFSafeTransportAcrossAttempts(t *testing.T) {
	factory := NewProviderFactory(security.SSRFPolicy{})
	clients := make(chan any, 64)
	var group sync.WaitGroup
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			client, err := factory.Client(Candidate{})
			if err != nil {
				t.Errorf("Client() error = %v", err)
				return
			}
			clients <- client
		}()
	}
	group.Wait()
	close(clients)
	var first any
	for client := range clients {
		if first == nil {
			first = client
			continue
		}
		if client != first {
			t.Fatal("Provider attempts received different HTTP connection pools")
		}
	}
}
