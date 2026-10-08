package main

import (
	"context"
	"flag"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"my-bookmark/internal/store"
)

func main() {
	project := flag.String("project", "", "Google Cloud project to migrate")
	flag.Parse()
	if *project == "" {
		log.Fatal("--project is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	c, err := firestore.NewClient(ctx, *project)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	n, err := store.New(c).InitializeRatingTotals(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Initialized rating totals for %d articles", n)
}
