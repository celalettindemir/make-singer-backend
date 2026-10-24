package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/identity"
	"github.com/redis/go-redis/v9"
)

// TaskTypeAccountCleanup is the task that cleans the deleted account's R2
// prefixes one more time, after a delay.
const TaskTypeAccountCleanup = "hesap:temizle"

// ErrAccountNotFound: the account to delete was not found (or the id is invalid).
var ErrAccountNotFound = errors.New("account not found")

// delayedCleanupDelay is how long after the deletion the second R2 cleanup
// runs. It is far longer than the longest job attempt (render default 30 min
// timeout). Attempts after the deletion upload nothing anyway: the workers
// check the job owner's deleted mark (lives 24 hours) before every paid
// call and every R2 write.
const delayedCleanupDelay = time.Hour

// AccountDeleter is the identity provider's account deletion capability
// (implemented by *identity.Server).
type AccountDeleter interface {
	DeleteAccount(ctx context.Context, userID string) error
}

// Enqueuer is the part of *asynq.Client used here.
type Enqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// SocialCleaner is the part of the Discover data that is cleaned on account
// deletion (implemented by *social.PostgresStore).
type SocialCleaner interface {
	// ListPublicationIDs returns all publication ids, including soft-deleted ones.
	ListPublicationIDs(ctx context.Context, userID string) ([]string, error)
	// DeleteUserTraces deletes everything EXCEPT publications (profile, likes,
	// comments, follows, reports) in one transaction. Read paths join on the
	// profile, so the user becomes invisible immediately.
	DeleteUserTraces(ctx context.Context, userID string) error
	// DeleteUserData deletes the profile, publications, likes, comments,
	// follows and reports in one transaction.
	DeleteUserData(ctx context.Context, userID string) error
}

// CreditCleaner removes the user's credit data and anonymises their store
// records (implemented by *credit.PostgresStore). It must be idempotent: the
// delayed cleanup task runs it again.
type CreditCleaner interface {
	DeleteUserData(ctx context.Context, userID string) error
}

// JobsCleaner removes the user's background jobs, their events and push
// devices (implemented by *jobs.PostgresStore). Idempotent.
type JobsCleaner interface {
	DeleteOwner(ctx context.Context, userID string) error
}

type AccountService struct {
	identity      AccountDeleter
	prefixDeleter client.PrefixDeleter // nil: R2 not configured
	redis         *redis.Client
	queue         Enqueuer
	social        SocialCleaner // nil: no social module
	credit        CreditCleaner // nil: no credit module
	jobs          JobsCleaner   // nil: no jobs store
}

func NewAccountService(accountDeleter AccountDeleter, prefixDeleter client.PrefixDeleter, rdb *redis.Client, queue Enqueuer, social SocialCleaner) *AccountService {
	return &AccountService{identity: accountDeleter, prefixDeleter: prefixDeleter, redis: rdb, queue: queue, social: social}
}

// WithCreditCleaner enables credit data cleanup and returns the service.
func (s *AccountService) WithCreditCleaner(c CreditCleaner) *AccountService {
	s.credit = c
	return s
}

// WithJobsCleaner enables job and device cleanup and returns the service.
func (s *AccountService) WithJobsCleaner(c JobsCleaner) *AccountService {
	s.jobs = c
	return s
}

// Delete deletes the account and all its files. Once the identity record is
// gone the user cannot repeat the request (their token is rejected from then
// on), so errors of the following steps are logged but do not fail the request.
// The delayed task completes any R2 cleanup left unfinished.
func (s *AccountService) Delete(ctx context.Context, userID string) error {
	if _, err := client.UserPrefixes(userID); err != nil {
		return ErrAccountNotFound
	}
	if err := s.identity.DeleteAccount(ctx, userID); err != nil {
		if errors.Is(err, identity.ErrUserNotFound) {
			return ErrAccountNotFound
		}
		return fmt.Errorf("could not delete the account: %w", err)
	}

	if err := s.redis.Set(ctx, deletionmark.Key(userID), "1", deletionmark.TTL).Err(); err != nil {
		log.Printf("Warning: could not write the deleted marker (user %s): %v", userID, err)
	}
	if err := s.Cleanup(ctx, userID); err != nil {
		log.Printf("Warning: the account R2 cleanup is incomplete, the delayed task will finish it (user %s): %v", userID, err)
	}

	payload, err := json.Marshal(map[string]string{"userId": userID})
	if err != nil {
		log.Printf("Warning: could not prepare the cleanup task (user %s): %v", userID, err)
		return nil
	}
	if _, err := s.queue.Enqueue(asynq.NewTask(TaskTypeAccountCleanup, payload),
		asynq.Queue("bakim"),
		asynq.ProcessIn(delayedCleanupDelay),
		asynq.MaxRetry(10),
	); err != nil {
		log.Printf("Warning: could not set up the delayed cleanup task (user %s): %v", userID, err)
	}
	return nil
}

// Cleanup deletes all of the user's R2 prefixes, Discover data and credit data. Without R2
// the prefixes are skipped. If one prefix fails the others are still tried.
func (s *AccountService) Cleanup(ctx context.Context, userID string) error {
	prefixes, err := client.UserPrefixes(userID)
	if err != nil {
		return err
	}
	var errs []error
	if s.prefixDeleter != nil {
		for _, prefix := range prefixes {
			if _, err := s.prefixDeleter.DeletePrefix(ctx, prefix); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", prefix, err))
			}
		}
	}
	if s.social != nil {
		if err := s.cleanupSocial(ctx, userID); err != nil {
			errs = append(errs, err)
		}
	}
	if s.credit != nil {
		if err := s.credit.DeleteUserData(ctx, userID); err != nil {
			errs = append(errs, fmt.Errorf("could not delete credit data: %w", err))
		}
	}
	if s.jobs != nil {
		if err := s.jobs.DeleteOwner(ctx, userID); err != nil {
			errs = append(errs, fmt.Errorf("could not delete jobs and devices: %w", err))
		}
	}
	return errors.Join(errs...)
}

// cleanupSocial first deletes the profile and other traces (so the user is
// invisible even during an R2 outage), then each publication's social/{id}/
// prefix, and the publication rows last. If even one prefix fails the
// publication rows REMAIN: the delayed task lists the same ids again and
// completes the cleanup (idempotent). A trace deletion error does not stop
// the R2 loop.
func (s *AccountService) cleanupSocial(ctx context.Context, userID string) error {
	var errs []error
	if err := s.social.DeleteUserTraces(ctx, userID); err != nil {
		errs = append(errs, fmt.Errorf("could not delete social traces: %w", err))
	}
	ids, err := s.social.ListPublicationIDs(ctx, userID)
	if err != nil {
		errs = append(errs, fmt.Errorf("could not list social publications: %w", err))
		return errors.Join(errs...)
	}
	if s.prefixDeleter != nil {
		prefixFailed := false
		for _, id := range ids {
			prefix, err := client.SocialPrefix(id)
			if err != nil {
				errs = append(errs, fmt.Errorf("social prefix %s: %w", id, err))
				prefixFailed = true
				continue
			}
			if _, err := s.prefixDeleter.DeletePrefix(ctx, prefix); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", prefix, err))
				prefixFailed = true
			}
		}
		if prefixFailed {
			return errors.Join(errs...)
		}
	}
	if err := s.social.DeleteUserData(ctx, userID); err != nil {
		errs = append(errs, fmt.Errorf("could not delete social data: %w", err))
	}
	return errors.Join(errs...)
}
