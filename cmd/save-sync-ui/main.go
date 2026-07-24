package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"savesyncpspc/internal/games"
	"savesyncpspc/internal/ui"
)

var version = "dev"

func main() {
	host := flag.String("host", "127.0.0.1", "Host to bind")
	port := flag.Int("port", 8765, "Port to bind")
	gamesDir := flag.String("games-dir", games.DefaultGamesDir(), "Game metadata directory")
	showVersion := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("Save Sync PS-PC UI: http://%s/\n", addr)
	if err := http.ListenAndServe(addr, ui.Server{GamesDir: *gamesDir}.Handler()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
