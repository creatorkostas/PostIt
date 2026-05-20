package main

import (
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"sort"
)

// AppDeps holds the shared dependencies used by both GUI and Web modes.
type AppDeps struct {
	Store      *storage.Manager
	Processor  *processor.ScriptProcessor
	Client     *api.Client
	Collection models.Collection
	FlatList   []models.RequestInfo
}

// initApp initializes storage, processor, API client, and loads cached requests
// into a collection. outputDir is the base directory for all data files.
func initApp(outputDir string) (*AppDeps, error) {
	store := storage.NewManager(outputDir)
	if err := store.Init(); err != nil {
		return nil, err
	}

	proc := processor.NewScriptProcessor(store)
	client := api.NewClient(store, proc)

	cache := store.LoadCache()
	finalRequests := make([]models.RequestInfo, 0, len(cache))

	collection := models.Collection{
		Info: models.Info{Name: "Local Collection"},
	}
	for _, req := range cache {
		finalRequests = append(finalRequests, req)
	}
	sort.Slice(finalRequests, func(i, j int) bool {
		return finalRequests[i].Order < finalRequests[j].Order
	})
	collection.Item = models.ReconstructItems(finalRequests)

	return &AppDeps{
		Store:      store,
		Processor:  proc,
		Client:     client,
		Collection: collection,
		FlatList:   finalRequests,
	}, nil
}
