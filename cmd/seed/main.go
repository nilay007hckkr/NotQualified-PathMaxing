// Command seed fills a PathMaxing server with demo traffic by sending real
// HTTP requests: it signs in (or signs up) through the API, creates demo
// campaigns and links, then visits those links through the redirect
// endpoint with a realistic mix of people, link previews, email scanners and
// crawlers. Nothing is written to the database directly, so every number on
// the dashboard comes from the same code paths real visitors use.
//
//	go run ./cmd/seed -base http://localhost:8080 -visits 400
//
// To see different simulated visitors as different uniques when seeding a
// local server, start it with TRUSTED_PROXY_HOPS=1: the seed sends each
// visit with its own X-Forwarded-For address.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"time"
)

func main() {
	var cfg config
	flag.StringVar(&cfg.Base, "base", "http://localhost:8080", "server base URL (its BASE_URL)")
	flag.StringVar(&cfg.Email, "email", "demo@pathmaxing.example", "demo account email (created if missing)")
	flag.StringVar(&cfg.Password, "password", "", "demo account password (or SEED_PASSWORD; at least 8 characters)")
	flag.StringVar(&cfg.Name, "name", "Demo Team", "demo account name, used when signing up")
	flag.IntVar(&cfg.Visits, "visits", 400, "number of visits to send")
	flag.IntVar(&cfg.Workers, "workers", 8, "visits sent at once")
	flag.Uint64Var(&cfg.Seed, "seed", uint64(time.Now().UnixNano()), "random seed, for a repeatable mix")
	flag.Parse()
	if cfg.Password == "" {
		cfg.Password = os.Getenv("SEED_PASSWORD")
	}
	if len(cfg.Password) < 8 {
		fmt.Fprintln(os.Stderr, "seed: give the demo account a password of at least 8 characters with -password or SEED_PASSWORD")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	sum, err := run(ctx, cfg, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	fmt.Println(sum)
}

type config struct {
	Base     string
	Email    string
	Password string
	Name     string
	Visits   int
	Workers  int
	Seed     uint64
}

func newRand(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) }
