package computer

import (
	"fmt"
	"strings"
)

// Remote is the bastion's view of one computer. Implementations use the
// passwordless sudo account. PasswordMatches must be a single PAM check on
// the target, with a timeout, and must not log the password.
type Remote interface {
	UserExists(username string) (bool, error)
	PasswordMatches(username, password string) (bool, error)
	CreateUser(username, password string) error
	// DeleteUser compensates a create that did not finish. It must not run
	// for an account we did not just create.
	DeleteUser(username string) error
	WriteFiles(username string, files CredentialFiles) error
	InstallDaemon(username string) error
}

// Attempt is the counter while a key lock is held.
// Reserve consumes one attempt. allowed is false when already at the limit.
// Refund returns an attempt that was not a password guess.
// Reset clears the counter after a successful match.
type Attempt interface {
	Reserve(limit int) (n int, allowed bool, err error)
	Refund() error
	Reset() error
}

// AttemptStore locks one computer+username for the whole provision, including
// the exists-check and CreateUser. FileStore uses flock so two processes
// share the lock and the counter. Implementations must not store the password.
type AttemptStore interface {
	WithKey(computerID, username string, fn func(Attempt) error) error
}

// Request is one click of "开通" after the settings page is saved.
type Request struct {
	ComputerID    string
	ServerURL     string
	Username      string
	Password      string
	GitName       string
	GitEmail      string
	GitLabURL     string
	GitSSHKey     string
	GitKnownHosts string
	GitLabToken   string
	ModelEnv      string
	MulticaPAT    string
	FailureLimit  int
	AccountOnly   bool
	PreserveFiles bool
	Step          func(string)
	// AfterAuthenticate runs under the account lock after a successful PAM check.
	AfterAuthenticate func() error
}

// Apply runs rule A.
// WithKey holds the computer+username lock across reserve, PAM, and create.
// A PAM or SSH error refunds the reservation; only an explicit mismatch keeps it.
// Create is rolled back if writing files or installing the daemon fails.
func Apply(r Remote, store AttemptStore, req Request) (Result, error) {
	secrets := []string{req.Password, req.GitLabToken, req.MulticaPAT, req.GitSSHKey}
	for _, line := range strings.Split(req.ModelEnv, "\n") {
		if _, val, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			secrets = append(secrets, val)
		}
	}
	if err := validateUsername(req.Username); err != nil {
		return Result{}, SafeError(err, secrets...)
	}
	var files CredentialFiles
	var err error
	if !req.AccountOnly && !req.PreserveFiles {
		files, err = RenderFiles(req.ServerURL, req.GitName, req.GitEmail, req.GitLabURL, req.GitLabToken, req.ModelEnv, req.MulticaPAT)
		if err != nil {
			return Result{}, SafeError(err, secrets...)
		}
		files.GitSSHKey = req.GitSSHKey
		files.GitKnownHosts = req.GitKnownHosts
		if req.GitSSHKey != "" {
			files.Gitconfig += "[core]\n\tsshCommand = \"ssh -i ~/.config/multica-provision/git.key -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=~/.config/multica-provision/known_hosts\"\n"
		}
	}
	limit := req.FailureLimit
	if limit <= 0 {
		limit = defaultFailureLimit
	}
	var result Result
	err = store.WithKey(req.ComputerID, req.Username, func(attempt Attempt) error {
		var stepErr error
		result, stepErr = applyLocked(r, attempt, req, files, limit, secrets)
		return stepErr
	})
	if err != nil {
		return Result{}, SafeError(err, secrets...)
	}
	return result, nil
}

func applyLocked(r Remote, attempt Attempt, req Request, files CredentialFiles, limit int, secrets []string) (res Result, err error) {
	failures, allowed, err := attempt.Reserve(limit)
	if err != nil {
		return Result{}, SafeError(err, secrets...)
	}
	if !allowed {
		return Result{Action: ActionLocked, Reason: "too many failed password checks", Failures: failures}, nil
	}
	// Any exit that is not an explicit password mismatch refunds the reservation.
	keep := false
	defer func() {
		if keep {
			return
		}
		if rerr := attempt.Refund(); rerr != nil {
			refundErr := fmt.Errorf("refund failed, attempt count may still be charged: %w", rerr)
			if err != nil {
				err = fmt.Errorf("%v; %w", err, refundErr)
			} else {
				err = refundErr
			}
		}
	}()

	step := func(name string) {
		if req.Step != nil {
			req.Step(name)
		}
	}
	step("checking_account")
	exists, err := r.UserExists(req.Username)
	if err != nil {
		return Result{}, SafeError(err, secrets...)
	}
	if !exists && req.PreserveFiles {
		return Result{}, fmt.Errorf("Linux account does not exist")
	}
	ok := false
	if !exists {
		if err := attempt.Refund(); err != nil {
			return Result{}, SafeError(err, secrets...)
		}
		keep = true
		failures = 0
	} else {
		ok, err = r.PasswordMatches(req.Username, req.Password)
		if err != nil {
			return Result{}, SafeError(err, secrets...)
		}
	}
	res, err = Decide(Input{
		Username:     req.Username,
		UserExists:   exists,
		PasswordOK:   ok,
		Failures:     0,
		FailureLimit: limit,
	})
	if err != nil {
		return Result{}, SafeError(err, secrets...)
	}
	if res.Action == ActionRename || res.Action == ActionLocked {
		keep = true
		res.Failures = failures
		if failures >= limit {
			res.Action = ActionLocked
			res.Reason = "password did not match and the attempt limit was reached"
		}
		return res, nil
	}

	if exists && req.AfterAuthenticate != nil {
		if err := req.AfterAuthenticate(); err != nil {
			return Result{}, err
		}
	}

	step("preparing_daemon")
	if preparer, ok := r.(interface{ Prepare() error }); ok && !req.AccountOnly {
		if err := preparer.Prepare(); err != nil {
			return Result{}, err
		}
	}
	created := false
	if res.Action == ActionCreate {
		step("installing_packages")
		if err := r.CreateUser(req.Username, req.Password); err != nil {
			return Result{}, SafeError(err, secrets...)
		}
		created = true
	}
	if req.AccountOnly {
		if err := attempt.Reset(); err != nil {
			return Result{}, err
		}
		keep = true
		res.Failures = 0
		return res, nil
	}
	if !req.PreserveFiles {
		step("writing_configuration")
		if err := r.WriteFiles(req.Username, files); err != nil {
			if created {
				if derr := r.DeleteUser(req.Username); derr != nil {
					return Result{}, SafeError(fmt.Errorf("write credentials: %v; rollback delete failed, linux user may remain: %w", err, derr), secrets...)
				}
			}
			return Result{}, SafeError(fmt.Errorf("write credentials: %w", err), secrets...)
		}
	}
	step("refreshing_daemon")
	if err := r.InstallDaemon(req.Username); err != nil {
		if created {
			if derr := r.DeleteUser(req.Username); derr != nil {
				return Result{}, SafeError(fmt.Errorf("install daemon: %v; rollback delete failed, linux user may remain: %w", err, derr), secrets...)
			}
		}
		return Result{}, SafeError(fmt.Errorf("install daemon: %w", err), secrets...)
	}
	if err := attempt.Reset(); err != nil {
		if rerr := attempt.Refund(); rerr != nil {
			keep = true
			return Result{}, SafeError(fmt.Errorf("reset failed (%v); refund also failed, attempt count remains charged: %w", err, rerr), secrets...)
		}
		keep = true
		return Result{}, SafeError(fmt.Errorf("reset failed (%v); refunded, attempt count was not left charged", err), secrets...)
	}
	keep = true
	res.Failures = 0
	return res, nil
}
