package push

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"momo-poo/internal/store"
	"momo-poo/internal/trips"
)

type subscriptionStore interface {
	SavePushSubscription(context.Context, store.PushSubscription) error
	DeletePushSubscription(context.Context, string) error
	ListPushSubscriptions(context.Context) ([]store.PushSubscription, error)
}

// Service manages subscriptions and delivers Web Push notifications.
type Service struct {
	store      subscriptionStore
	publicKey  string
	privateKey string
	subscriber string
}

// New creates a Web Push service using a persistent VAPID key pair.
func New(store subscriptionStore, publicKey, privateKey, subscriber string) *Service {
	return &Service{store: store, publicKey: publicKey, privateKey: privateKey, subscriber: subscriber}
}

func (s *Service) PublicKey() string { return s.publicKey }

func (s *Service) Subscribe(ctx context.Context, subscription store.PushSubscription) error {
	return s.store.SavePushSubscription(ctx, subscription)
}

func (s *Service) Unsubscribe(ctx context.Context, endpoint string) error {
	return s.store.DeletePushSubscription(ctx, endpoint)
}

// NotifyTrip sends one notification to every subscribed device.
func (s *Service) NotifyTrip(ctx context.Context, trip trips.Trip, location *time.Location) error {
	subscriptions, err := s.store.ListPushSubscriptions(ctx)
	if err != nil {
		return err
	}
	if len(subscriptions) == 0 {
		return nil
	}

	body := "Pee-only trip"
	if trip.HasPoo {
		body = "Pee + poo trip"
	}
	payload, err := json.Marshal(map[string]string{
		"title": "Momo went outside",
		"body":  body + " logged at " + trip.OccurredAt.In(location).Format("3:04 PM"),
		"url":   "/",
	})
	if err != nil {
		return fmt.Errorf("push: encode payload: %w", err)
	}

	var wg sync.WaitGroup
	errors := make(chan error, len(subscriptions))
	for _, subscription := range subscriptions {
		wg.Add(1)
		go func(subscription store.PushSubscription) {
			defer wg.Done()
			response, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
				Endpoint: subscription.Endpoint,
				Keys:     webpush.Keys{P256dh: subscription.P256DH, Auth: subscription.Auth},
			}, &webpush.Options{
				Subscriber: s.subscriber, VAPIDPublicKey: s.publicKey, VAPIDPrivateKey: s.privateKey,
				TTL: 60 * 60, Urgency: webpush.UrgencyHigh,
			})
			if err != nil {
				errors <- fmt.Errorf("send to push service: %w", err)
				return
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)
			if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
				deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				defer cancel()
				if err := s.store.DeletePushSubscription(deleteCtx, subscription.Endpoint); err != nil {
					errors <- err
				}
				return
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				errors <- fmt.Errorf("push service returned %s", response.Status)
			}
		}(subscription)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		return err
	}
	return nil
}
