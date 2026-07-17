package buzzer

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSnapshotDoesNotExposeMutableFirstBuzzPointer(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	player, _ := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", "")
	buzzed, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken)
	if err != nil {
		t.Fatal(err)
	}
	buzzed.Snapshot.FirstBuzz.PlayerName = "mutated outside store"

	snapshot, err := store.Snapshot(group.Code)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FirstBuzz.PlayerName != "Player" {
		t.Fatalf("snapshot mutation leaked into store: %+v", snapshot.FirstBuzz)
	}
}

func TestCreateGroupHasActiveGroupLimit(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	for i := 0; i < maxActiveGroups; i++ {
		if _, err := store.CreateGroup("Host", "#ff4d6d"); err != nil {
			t.Fatalf("create group %d: %v", i, err)
		}
	}
	if _, err := store.CreateGroup("Host", "#ff4d6d"); err != ErrLimit {
		t.Fatalf("create over limit err = %v, want ErrLimit", err)
	}
}

func TestJoinGroupHasPlayerLimit(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, err := store.CreateGroup("Host", "#ff4d6d")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxPlayersPerGroup; i++ {
		if _, err := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", ""); err != nil {
			t.Fatalf("join player %d: %v", i, err)
		}
	}
	if _, err := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", ""); err != ErrLimit {
		t.Fatalf("join over limit err = %v, want ErrLimit", err)
	}
}

func TestCreateJoinReconnectAndSnapshot(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	result, err := store.CreateGroup("Mira", "#ff4d6d")
	if err != nil {
		t.Fatal(err)
	}
	if result.Code == "" || result.HostToken == "" || result.PlayerToken == "" {
		t.Fatalf("missing session details: %+v", result)
	}
	if len(result.Snapshot.Players) != 1 || !result.Snapshot.Players[0].IsHost {
		t.Fatalf("host should be the first player: %+v", result.Snapshot.Players)
	}

	joined, err := store.JoinGroup(result.Code, "Bo", "#2ec4b6", "", "")
	if err != nil {
		t.Fatal(err)
	}
	rejoined, err := store.JoinGroup(result.Code, "Bo Prime", "#f9c74f", joined.PlayerID, joined.PlayerToken)
	if err != nil {
		t.Fatal(err)
	}
	if rejoined.PlayerID != joined.PlayerID {
		t.Fatalf("reconnect created a new player: got %s want %s", rejoined.PlayerID, joined.PlayerID)
	}
	if got := playerByID(rejoined.Snapshot, joined.PlayerID).Name; got != "Bo Prime" {
		t.Fatalf("reconnect did not update player name: %q", got)
	}
}

func TestFirstBuzzWinsEvenWithConcurrentPresses(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	alfa, _ := store.JoinGroup(group.Code, "Alfa", "#2ec4b6", "", "")
	bravo, _ := store.JoinGroup(group.Code, "Bravo", "#f9c74f", "", "")

	var wg sync.WaitGroup
	results := make(chan BuzzResult, 2)
	for _, player := range []JoinResult{alfa, bravo} {
		wg.Add(1)
		go func(player JoinResult) {
			defer wg.Done()
			result, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken)
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		}(player)
	}
	wg.Wait()
	close(results)

	accepted := 0
	for result := range results {
		if result.Accepted {
			accepted++
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted buzzes = %d, want 2", accepted)
	}
	snapshot, _ := store.Snapshot(group.Code)
	if snapshot.FirstBuzz == nil || len(snapshot.Buzzes) != 2 {
		t.Fatalf("bad buzz state: %+v", snapshot)
	}
	if snapshot.FirstBuzz.PlayerID != snapshot.Buzzes[0].PlayerID {
		t.Fatalf("first buzz = %+v, first order entry = %+v", snapshot.FirstBuzz, snapshot.Buzzes[0])
	}
	if snapshot.Buzzes[0].Order != 1 || snapshot.Buzzes[1].Order != 2 {
		t.Fatalf("bad buzz order: %+v", snapshot.Buzzes)
	}
}

func TestPlayerCanBuzzOnlyOncePerRound(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	player, _ := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", "")

	first, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken)
	if err != nil || !first.Accepted {
		t.Fatalf("first buzz = %+v err=%v, want accepted", first, err)
	}
	second, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken)
	if err != nil || second.Accepted || second.Reason != "already buzzed this round" {
		t.Fatalf("second buzz = %+v err=%v, want duplicate rejection", second, err)
	}
}

func TestUpdatePlayerRenamesCurrentBuzzEntries(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	player, _ := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", "")
	if _, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken); err != nil {
		t.Fatal(err)
	}

	updated, err := store.UpdatePlayer(group.Code, player.PlayerID, player.PlayerToken, "Player Two", "#f9c74f")
	if err != nil {
		t.Fatal(err)
	}
	if got := playerByID(updated.Snapshot, player.PlayerID); got.Name != "Player Two" || got.Color != "#f9c74f" {
		t.Fatalf("profile not updated in player list: %+v", got)
	}
	if updated.Snapshot.FirstBuzz.PlayerName != "Player Two" || updated.Snapshot.Buzzes[0].Color != "#f9c74f" {
		t.Fatalf("profile not updated in buzz list: %+v", updated.Snapshot)
	}
}

func TestRemovePlayerDropsBuzzAndRenumbersOrder(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	alfa, _ := store.JoinGroup(group.Code, "Alfa", "#2ec4b6", "", "")
	bravo, _ := store.JoinGroup(group.Code, "Bravo", "#f9c74f", "", "")
	charlie, _ := store.JoinGroup(group.Code, "Charlie", "#4361ee", "", "")
	_, _ = store.Buzz(group.Code, alfa.PlayerID, alfa.PlayerToken)
	_, _ = store.Buzz(group.Code, bravo.PlayerID, bravo.PlayerToken)
	_, _ = store.Buzz(group.Code, charlie.PlayerID, charlie.PlayerToken)

	snapshot, err := store.RemovePlayer(group.Code, group.HostToken, alfa.PlayerID)
	if err != nil {
		t.Fatal(err)
	}
	if playerByID(snapshot, alfa.PlayerID).ID != "" {
		t.Fatalf("removed player still present: %+v", snapshot.Players)
	}
	if snapshot.FirstBuzz.PlayerID != bravo.PlayerID || len(snapshot.Buzzes) != 2 {
		t.Fatalf("bad buzzes after removing first player: %+v", snapshot.Buzzes)
	}
	if snapshot.Buzzes[0].Order != 1 || snapshot.Buzzes[1].Order != 2 {
		t.Fatalf("buzz order not renumbered: %+v", snapshot.Buzzes)
	}
}

func TestHostCannotRemoveSelf(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	if _, err := store.RemovePlayer(group.Code, group.HostToken, group.PlayerID); err != ErrInvalid {
		t.Fatalf("remove host err = %v, want ErrInvalid", err)
	}
}

func TestAnswersRevealOnlyToHostAfterEveryoneSubmits(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	alfa, _ := store.JoinGroup(group.Code, "Alfa", "#2ec4b6", "", "")
	bravo, _ := store.JoinGroup(group.Code, "Bravo", "#f9c74f", "", "")
	if _, err := store.SetMode(group.Code, group.HostToken, ModeAnswers); err != nil {
		t.Fatal(err)
	}

	first, err := store.SubmitAnswer(group.Code, alfa.PlayerID, alfa.PlayerToken, "  Alfa's answer  ")
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot.AnswersRevealed || first.Snapshot.SubmittedCount != 1 || first.Snapshot.ExpectedAnswerCount != 2 {
		t.Fatalf("answers revealed early: %+v", first.Snapshot)
	}
	if answers, err := store.HostAnswers(group.Code, group.HostToken); err != nil || len(answers.Answers) != 0 {
		t.Fatalf("host saw answers before reveal: %+v err=%v", answers, err)
	}

	second, err := store.SubmitAnswer(group.Code, bravo.PlayerID, bravo.PlayerToken, "Bravo's answer")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Snapshot.AnswersRevealed {
		t.Fatalf("answers were not revealed: %+v", second.Snapshot)
	}
	answers, err := store.HostAnswers(group.Code, group.HostToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(answers.Answers) != 2 || answers.Answers[0].Text != "Alfa's answer" || answers.Answers[1].Text != "Bravo's answer" {
		t.Fatalf("unexpected host answers: %+v", answers.Answers)
	}
	if _, err := store.SubmitAnswer(group.Code, alfa.PlayerID, alfa.PlayerToken, "too late"); err != ErrInvalid {
		t.Fatalf("answer after reveal err = %v, want ErrInvalid", err)
	}
	public, _ := store.Snapshot(group.Code)
	encoded := fmt.Sprintf("%+v", public)
	if strings.Contains(encoded, "Alfa's answer") || strings.Contains(encoded, "Bravo's answer") {
		t.Fatalf("public snapshot exposed answer text: %s", encoded)
	}
	reset, err := store.Reset(group.Code, group.HostToken)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Mode != ModeAnswers || reset.AnswersRevealed || reset.SubmittedCount != 0 || reset.Round != 2 {
		t.Fatalf("answer round did not reset cleanly: %+v", reset)
	}
}

func TestKickingLastNonSubmitterRevealsRemainingAnswers(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	answered, _ := store.JoinGroup(group.Code, "Answered", "#2ec4b6", "", "")
	waiting, _ := store.JoinGroup(group.Code, "Waiting", "#f9c74f", "", "")
	_, _ = store.SetMode(group.Code, group.HostToken, ModeAnswers)
	_, _ = store.SubmitAnswer(group.Code, answered.PlayerID, answered.PlayerToken, "Keep me")

	snapshot, err := store.RemovePlayer(group.Code, group.HostToken, waiting.PlayerID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.AnswersRevealed || snapshot.SubmittedCount != 1 || snapshot.ExpectedAnswerCount != 1 {
		t.Fatalf("kick did not trigger reveal: %+v", snapshot)
	}
	answers, _ := store.HostAnswers(group.Code, group.HostToken)
	if len(answers.Answers) != 1 || answers.Answers[0].PlayerID != answered.PlayerID {
		t.Fatalf("wrong answers after kick: %+v", answers.Answers)
	}
}

func TestKickingSubmittedPlayerRemovesTheirRevealedAnswer(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	alfa, _ := store.JoinGroup(group.Code, "Alfa", "#2ec4b6", "", "")
	bravo, _ := store.JoinGroup(group.Code, "Bravo", "#f9c74f", "", "")
	_, _ = store.SetMode(group.Code, group.HostToken, ModeAnswers)
	_, _ = store.SubmitAnswer(group.Code, alfa.PlayerID, alfa.PlayerToken, "Remove me")
	_, _ = store.SubmitAnswer(group.Code, bravo.PlayerID, bravo.PlayerToken, "Keep me")

	snapshot, err := store.RemovePlayer(group.Code, group.HostToken, alfa.PlayerID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.AnswersRevealed || snapshot.SubmittedCount != 1 {
		t.Fatalf("reveal state changed after kick: %+v", snapshot)
	}
	answers, _ := store.HostAnswers(group.Code, group.HostToken)
	if len(answers.Answers) != 1 || answers.Answers[0].PlayerID != bravo.PlayerID || answers.Answers[0].Text != "Keep me" {
		t.Fatalf("kicked player's answer remains: %+v", answers.Answers)
	}
}

func TestVoluntaryLeaveRecalculatesAnswerRevealAndRemovesSubmission(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	answered, _ := store.JoinGroup(group.Code, "Answered", "#2ec4b6", "", "")
	waiting, _ := store.JoinGroup(group.Code, "Waiting", "#f9c74f", "", "")
	_, _ = store.SetMode(group.Code, group.HostToken, ModeAnswers)
	_, _ = store.SubmitAnswer(group.Code, answered.PlayerID, answered.PlayerToken, "Keep for now")

	snapshot, err := store.LeaveGroup(group.Code, waiting.PlayerID, waiting.PlayerToken)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.AnswersRevealed || snapshot.SubmittedCount != 1 || playerByID(snapshot, waiting.PlayerID).ID != "" {
		t.Fatalf("waiting player's leave did not reveal: %+v", snapshot)
	}

	snapshot, err = store.LeaveGroup(group.Code, answered.PlayerID, answered.PlayerToken)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.AnswersRevealed || snapshot.SubmittedCount != 0 || snapshot.ExpectedAnswerCount != 0 {
		t.Fatalf("submitted answer remained after leave: %+v", snapshot)
	}
	answers, _ := store.HostAnswers(group.Code, group.HostToken)
	if len(answers.Answers) != 0 {
		t.Fatalf("departed player's answer remains: %+v", answers.Answers)
	}
	if _, err := store.LeaveGroup(group.Code, group.PlayerID, group.PlayerToken); err != ErrInvalid {
		t.Fatalf("host leave err = %v, want ErrInvalid", err)
	}
}

func TestHeartbeatRefreshesPlayerPresence(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Hour)
	store.SetClock(func() time.Time { return now })
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	player, _ := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", "")

	now = now.Add(3 * time.Minute)
	snapshot, err := store.Heartbeat(group.Code, player.PlayerID, player.PlayerToken)
	if err != nil {
		t.Fatal(err)
	}
	if got := playerByID(snapshot, player.PlayerID); !got.LastSeenAt.Equal(now) || got.LastSeen != "just now" {
		t.Fatalf("heartbeat did not refresh presence: %+v", got)
	}
	if _, err := store.Heartbeat(group.Code, player.PlayerID, "wrong"); err != ErrUnauthorized {
		t.Fatalf("heartbeat err = %v, want ErrUnauthorized", err)
	}
}

func TestAnswerValidationAndPermissions(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	player, _ := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", "")
	if _, err := store.SubmitAnswer(group.Code, player.PlayerID, player.PlayerToken, "not in answer mode"); err != ErrInvalid {
		t.Fatalf("answer in buzzer mode err = %v, want ErrInvalid", err)
	}
	if _, err := store.SetMode(group.Code, group.HostToken, ModeAnswers); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitAnswer(group.Code, group.PlayerID, group.PlayerToken, "host answer"); err != ErrInvalid {
		t.Fatalf("host answer err = %v, want ErrInvalid", err)
	}
	if _, err := store.SubmitAnswer(group.Code, player.PlayerID, player.PlayerToken, "  "); err != ErrInvalid {
		t.Fatalf("blank answer err = %v, want ErrInvalid", err)
	}
	if _, err := store.SubmitAnswer(group.Code, player.PlayerID, player.PlayerToken, strings.Repeat("x", maxAnswerLength+1)); err != ErrInvalid {
		t.Fatalf("long answer err = %v, want ErrInvalid", err)
	}
	if _, err := store.HostAnswers(group.Code, "wrong"); err != ErrUnauthorized {
		t.Fatalf("host answers err = %v, want ErrUnauthorized", err)
	}
}

func TestLocksAndReset(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	player, _ := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", "")

	if _, err := store.SetPlayerLock(group.Code, group.HostToken, player.PlayerID, true); err != nil {
		t.Fatal(err)
	}
	if result, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken); err != nil || result.Accepted {
		t.Fatalf("locked player buzz = %+v err=%v, want rejected", result, err)
	}
	if _, err := store.SetPlayerLock(group.Code, group.HostToken, player.PlayerID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLockAll(group.Code, group.HostToken, true); err != nil {
		t.Fatal(err)
	}
	if result, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken); err != nil || result.Accepted {
		t.Fatalf("global lock buzz = %+v err=%v, want rejected", result, err)
	}
	if _, err := store.SetLockAll(group.Code, group.HostToken, false); err != nil {
		t.Fatal(err)
	}
	if result, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken); err != nil || !result.Accepted {
		t.Fatalf("unlocked buzz = %+v err=%v, want accepted", result, err)
	}
	snapshot, err := store.Reset(group.Code, group.HostToken)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FirstBuzz != nil || len(snapshot.Buzzes) != 0 || snapshot.Round != 2 {
		t.Fatalf("reset failed: %+v", snapshot)
	}
}

func TestBulkLockAllAppliesAndClearsIndividualLocks(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	player, _ := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", "")

	lockedSnapshot, err := store.SetLockAll(group.Code, group.HostToken, true)
	if err != nil {
		t.Fatal(err)
	}
	if !lockedSnapshot.LockedAll || !playerByID(lockedSnapshot, group.PlayerID).Locked || !playerByID(lockedSnapshot, player.PlayerID).Locked {
		t.Fatalf("bulk lock did not lock every player: %+v", lockedSnapshot)
	}

	if _, err := store.SetPlayerLock(group.Code, group.HostToken, player.PlayerID, true); err != nil {
		t.Fatal(err)
	}
	unlockedSnapshot, err := store.SetLockAll(group.Code, group.HostToken, false)
	if err != nil {
		t.Fatal(err)
	}
	if unlockedSnapshot.LockedAll || playerByID(unlockedSnapshot, group.PlayerID).Locked || playerByID(unlockedSnapshot, player.PlayerID).Locked {
		t.Fatalf("bulk unlock did not clear every lock: %+v", unlockedSnapshot)
	}
	if result, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken); err != nil || !result.Accepted {
		t.Fatalf("buzz after bulk unlock = %+v err=%v, want accepted", result, err)
	}
}

func TestResetRoundCountClearsBuzzesAndReturnsToOne(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	player, _ := store.JoinGroup(group.Code, "Player", "#2ec4b6", "", "")

	if _, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reset(group.Code, group.HostToken); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Buzz(group.Code, player.PlayerID, player.PlayerToken); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.ResetRoundCount(group.Code, group.HostToken)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Round != 1 || snapshot.FirstBuzz != nil || len(snapshot.Buzzes) != 0 {
		t.Fatalf("round count reset failed: %+v", snapshot)
	}
}

func TestHostTokenRequiredForControls(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, _ := store.CreateGroup("Host", "#ff4d6d")
	if _, err := store.Reset(group.Code, "wrong"); err != ErrUnauthorized {
		t.Fatalf("reset err = %v, want ErrUnauthorized", err)
	}
	if _, err := store.ResetRoundCount(group.Code, "wrong"); err != ErrUnauthorized {
		t.Fatalf("reset round count err = %v, want ErrUnauthorized", err)
	}
	if _, err := store.SetMode(group.Code, "wrong", ModeAnswers); err != ErrUnauthorized {
		t.Fatalf("set mode err = %v, want ErrUnauthorized", err)
	}
	if _, err := store.SetMode(group.Code, group.HostToken, "invalid"); err != ErrInvalid {
		t.Fatalf("invalid mode err = %v, want ErrInvalid", err)
	}
}

func TestSubscribeHasPerGroupLimit(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	group, err := store.CreateGroup("Host", "#ff4d6d")
	if err != nil {
		t.Fatal(err)
	}
	cancels := make([]func(), 0, maxSubscribersPerGroup)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for i := 0; i < maxSubscribersPerGroup; i++ {
		_, cancel, err := store.Subscribe(group.Code)
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		cancels = append(cancels, cancel)
	}
	if _, _, err := store.Subscribe(group.Code); err != ErrLimit {
		t.Fatalf("subscribe over limit err = %v, want ErrLimit", err)
	}
}

func TestGroupsExpireAndBecomeInaccessible(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Hour)
	store.SetClock(func() time.Time { return now })
	group, _ := store.CreateGroup("Host", "#ff4d6d")

	now = now.Add(61 * time.Minute)
	if _, err := store.Snapshot(group.Code); err != ErrExpired {
		t.Fatalf("snapshot err = %v, want ErrExpired", err)
	}
}

func playerByID(snapshot Snapshot, id string) PlayerSnapshot {
	for _, player := range snapshot.Players {
		if player.ID == id {
			return player
		}
	}
	return PlayerSnapshot{}
}
