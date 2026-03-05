package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"

	mr "github.com/aslakknutsen/make-reconcile"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	AddToScheme(scheme)

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Error("failed to build kubeconfig", "error", err)
		os.Exit(1)
	}

	mgr, err := mr.NewManager(cfg, scheme,
		mr.WithLogger(log),
		mr.WithManagerID("gatewayclass-controller"),
	)
	if err != nil {
		log.Error("failed to create manager", "error", err)
		os.Exit(1)
	}

	RegisterAll(mgr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.Info("starting gatewayclass controller")
	if err := mgr.Start(ctx); err != nil {
		log.Error("manager exited with error", "error", err)
		os.Exit(1)
	}
}
