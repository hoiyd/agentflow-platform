package hashembedding

import (
	"testing"

	"agentflow-platform/apps/api/internal/inference/provider"
)

// Freeze the original offline algorithm and identity, not semantic quality.
func TestOfflineEmbeddingContract(t *testing.T) {
	var client provider.Embedder = Embedder{}
	want := make([]float64, 1536)
	for _, index := range []int{754, 1466, 1456, 270, 744, 1322, 1182, 790, 92, 94, 260, 866, 548} {
		want[index] = -0.25
	}
	for _, index := range []int{1465, 423, 907} {
		want[index] = 0.25
	}
	for _, text := range []string{"hello", " HELLO ", "hello hello"} {
		got, err := client.EmbedText(t.Context(), text)
		if err != nil || got.Provider != "simulated" || got.Model != "local_hash_embedding" || !got.Estimated || got.Dimensions != len(want) || len(got.Vector) != len(want) {
			t.Fatalf("offline embedding identity: %+v %v", got, err)
		}
		for i, value := range want {
			if got.Vector[i] != value {
				t.Fatalf("algorithm changed at %d: got %g want %g", i, got.Vector[i], value)
			}
		}
	}
	if _, err := client.EmbedText(t.Context(), " \n "); err == nil {
		t.Fatal("blank embedding input accepted")
	}
}
