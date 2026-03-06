package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"

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

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Error("failed to create clientset", "error", err)
		os.Exit(1)
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme, corev1.EventSource{Component: "coraza-operator"})

	mgr, err := mr.NewManager(cfg, scheme,
		mr.WithLogger(log),
		mr.WithManagerID("coraza-operator"),
		mr.WithEventRecorder(recorder),
	)
	if err != nil {
		log.Error("failed to create manager", "error", err)
		os.Exit(1)
	}

	RegisterAll(mgr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.Info("starting coraza operator")
	if err := mgr.Start(ctx); err != nil {
		log.Error("manager exited with error", "error", err)
		os.Exit(1)
	}
}
