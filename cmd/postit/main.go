package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"postit/internal/tui"
	"postit/internal/ui"
	"postit/internal/web"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/charmbracelet/log"
)

func init() {
	// Seed random number generator for magic variables ($randomInt, etc.)
	rand.Seed(time.Now().UnixNano())
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		runCLI()
		return
	}

	uiType := flag.String("ui", "web", "UI type to use: tui, cli, or web")
	port := flag.Int("port", 8080, "Port for Web UI")
	enableMock := flag.Bool("mock", false, "Enable Mock Server (for Web UI)")
	importPaths := flag.String("import", "", "Paths to Postman collection JSON to import (comma-separated)")
	flag.Parse()

	store := storage.NewManager("output")
	if err := store.Init(); err != nil {
	        log.Fatal("Failed to initialize storage", "error", err)
	}

	proc := processor.NewScriptProcessor(store)
	client := api.NewClient(store, proc)

	var collection models.Collection
	cache := store.LoadCache()
	finalRequests := []models.RequestInfo{}

	if *importPaths != "" {
	        paths := strings.Split(*importPaths, ",")
	        orderCounter := 0
	        for _, path := range paths {
	                path = strings.TrimSpace(path)
	                if path == "" {
	                        continue
	                }
	                data, err := os.ReadFile(path)
	                if err != nil {
	                        log.Warn("Error reading import file", "path", path, "error", err)
	                        continue
	                }

	                _, requests, err := processor.ParsePostmanCollection(data)
	                if err != nil {
	                        log.Warn("Error parsing collection", "path", path, "error", err)
	                        continue
	                }

	                for _, fresh := range requests {
	                        fresh.Order = orderCounter
	                        orderCounter++
	                        finalRequests = append(finalRequests, fresh)
	                        store.SaveSingleRequest(fresh)
	                }
	        }
	        collection.Info.Name = "Imported Collections"
	        collection.Item = models.ReconstructItems(finalRequests)
	} else {
	        collection.Info.Name = "Local Collection"
	        for _, req := range cache {
	                finalRequests = append(finalRequests, req)
	        }
	        sort.Slice(finalRequests, func(i, j int) bool {
	                return finalRequests[i].Order < finalRequests[j].Order
	        })
	        collection.Item = models.ReconstructItems(finalRequests)
	}
	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Info("Shutdown signal received, cleaning up...")
		cancel()
		// Give components time to clean up
		time.Sleep(100 * time.Millisecond)
		// Close DB connections
		if client != nil {
			client.Close()
		}
		os.Exit(0)
	}()

	switch *uiType {
	case "web":
		server := web.NewServer(store, proc, client, collection, finalRequests, *enableMock)
		if err := server.Start(ctx, *port); err != nil {
			log.Fatal("Web Server Error", "error", err)
		}
	case "tui":
		tuiApp := tui.NewTUIApp(store, proc, client)
		if err := tuiApp.Run(collection, finalRequests); err != nil {
			log.Fatal("TUI Error", "error", err)
		}
	case "cli":
		cliMenu := ui.NewMenu(store, proc, client)
		runCLIMenu(cliMenu, &finalRequests)
	default:
		log.Fatal("Unknown UI type", "type", *uiType)
	}
}

func runCLI() {
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)
	collectionPath := runCmd.String("collection", "", "Path to collection JSON")
	envPath := runCmd.String("env", "", "Path to environment JSON")
	delay := runCmd.Int("delay", 0, "Delay between requests in ms")
	runCmd.Parse(os.Args[2:])

	if *collectionPath == "" {
		fmt.Println("Usage: postit run --collection <path> [--env <path>] [--delay <ms>]")
		return
	}

	store := storage.NewManager("output")
	store.Init()
	proc := processor.NewScriptProcessor(store)
	client := api.NewClient(store, proc)

	// Load Environment if provided
	if *envPath != "" {
		data, _ := os.ReadFile(*envPath)
		var env models.Environment
		json.Unmarshal(data, &env)
		store.Environments = append(store.Environments, env)
		store.ActiveEnvID = env.ID
	}

	// Load Collection
	data, err := os.ReadFile(*collectionPath)
	if err != nil {
		log.Fatal("Error reading collection", "error", err)
	}
	
	_, requests, err := processor.ParsePostmanCollection(data)
	if err != nil {
		log.Fatal("Error parsing collection", "error", err)
	}

	fmt.Printf("Running collection (%d requests)\n", len(requests))
	fmt.Println(strings.Repeat("-", 50))

	for _, req := range requests {
		fmt.Printf("RUNNING: %s [%s] %s\n", req.Path, req.Request.Method, req.Request.URL.Raw)

		proc.RunScripts(req.Events, "prerequest", nil, nil, req.Request.Header)
		body, _, code, status := client.ExecuteRequest(context.Background(), req.Request)

		color := "\033[32m"
		if code >= 400 {
			color = "\033[31m"
		}
		fmt.Printf("RESULT:  %s%d %s\033[0m\n", color, code, status)

		if body != "" {
			proc.RunScripts(req.Events, "test", []byte(body), nil, req.Request.Header)
		}

		if *delay > 0 {
			time.Sleep(time.Duration(*delay) * time.Millisecond)
		}
		fmt.Println()
	}
	fmt.Println("Done.")
}

func runCLIMenu(menu *ui.Menu, finalRequests *[]models.RequestInfo) {
	for {
		options := []string{"[Create New Request]", "[Manage Global Headers]", "[Manage Global Variables]"}
		for _, r := range *finalRequests {
			options = append(options, r.Path)
		}
		var selected string
		prompt := &survey.Select{Message: "Select a request or action:", Options: options, PageSize: 15}
		if err := survey.AskOne(prompt, &selected); err != nil {
			break
		}
		switch selected {
		case "[Create New Request]":
			if req := menu.CreateNewRequest(); req != nil {
				*finalRequests = append(*finalRequests, *req)
			}
		case "[Manage Global Headers]":
			menu.ManageGlobalHeaders()
		case "[Manage Global Variables]":
			menu.ViewVariables()
		default:
			for i, r := range *finalRequests {
				if r.Path == selected {
					menu.HandleRequestSelection(&(*finalRequests)[i], finalRequests)
					break
				}
			}
		}
		fmt.Println("\n--------------------------------------------------")
	}
}
