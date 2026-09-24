package db

import (
	"encoding/json"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// NewContestParams returns creation parameters with the same defaults as the
// schema, so callers only set what they care about.
func NewContestParams(name string, start, stop time.Time) sqlc.CreateContestParams {
	return sqlc.CreateContestParams{
		Name: name, AllowedLocalizations: []string{}, Languages: []string{},
		SubmissionsDownloadAllowed: true, AllowQuestions: true, AllowUserTests: true,
		AllowPasswordAuthentication: true, TokenMode: "disabled", TokenGenInitial: 2,
		TokenGenNumber: 2, TokenGenIntervalS: 1800, StartTime: start, StopTime: stop,
		Timezone: "UTC", ScoringMode: "ioi", IcpcPenaltyMinutes: 20, MaxPrintJobs: 10, MaxPrintPages: 20,
	}
}

// ContestToUpdate converts a row into update parameters (edit then save).
func ContestToUpdate(c sqlc.Contest) sqlc.UpdateContestParams {
	return sqlc.UpdateContestParams{
		ID: c.ID, Name: c.Name, Description: c.Description, AllowedLocalizations: c.AllowedLocalizations,
		Languages: c.Languages, SubmissionsDownloadAllowed: c.SubmissionsDownloadAllowed,
		AllowQuestions: c.AllowQuestions, AllowUserTests: c.AllowUserTests, AllowPrinting: c.AllowPrinting,
		BlockHiddenParticipations: c.BlockHiddenParticipations, AllowPasswordAuthentication: c.AllowPasswordAuthentication,
		IpRestriction: c.IpRestriction, IpAutologin: c.IpAutologin, SingleLogin: c.SingleLogin,
		TokenMode: c.TokenMode, TokenMaxNumber: c.TokenMaxNumber, TokenMinIntervalS: c.TokenMinIntervalS,
		TokenGenInitial: c.TokenGenInitial, TokenGenNumber: c.TokenGenNumber, TokenGenIntervalS: c.TokenGenIntervalS,
		TokenGenMax: c.TokenGenMax, StartTime: c.StartTime, StopTime: c.StopTime, AnalysisEnabled: c.AnalysisEnabled,
		AnalysisStart: c.AnalysisStart, AnalysisStop: c.AnalysisStop, Timezone: c.Timezone,
		PerUserTimeS: c.PerUserTimeS, MaxSubmissionNumber: c.MaxSubmissionNumber,
		MaxUserTestNumber: c.MaxUserTestNumber, MinSubmissionIntervalS: c.MinSubmissionIntervalS,
		MinUserTestIntervalS: c.MinUserTestIntervalS, ScorePrecision: c.ScorePrecision,
		ScoringMode: c.ScoringMode, IcpcPenaltyMinutes: c.IcpcPenaltyMinutes,
		RankingFreezeTime: c.RankingFreezeTime, MaxPrintJobs: c.MaxPrintJobs, MaxPrintPages: c.MaxPrintPages,
	}
}

// NewTaskParams returns task creation parameters with schema defaults and a
// single-file submission format.
func NewTaskParams(name, title string) sqlc.CreateTaskParams {
	return sqlc.CreateTaskParams{
		Name: name, Title: title, PrimaryStatements: []string{}, SubmissionFormat: []string{name + ".%l"},
		TokenMode: "disabled", TokenGenInitial: 2, TokenGenNumber: 2, TokenGenIntervalS: 1800,
		FeedbackLevel: "full", ScoreMode: "max_subtask",
	}
}

// TaskToUpdate converts a row into update parameters.
func TaskToUpdate(t sqlc.Task) sqlc.UpdateTaskParams {
	return sqlc.UpdateTaskParams{
		ID: t.ID, ContestID: t.ContestID, Num: t.Num, Name: t.Name, Title: t.Title,
		PrimaryStatements: t.PrimaryStatements, SubmissionFormat: t.SubmissionFormat, TokenMode: t.TokenMode,
		TokenMaxNumber: t.TokenMaxNumber, TokenMinIntervalS: t.TokenMinIntervalS, TokenGenInitial: t.TokenGenInitial,
		TokenGenNumber: t.TokenGenNumber, TokenGenIntervalS: t.TokenGenIntervalS, TokenGenMax: t.TokenGenMax,
		MaxSubmissionNumber: t.MaxSubmissionNumber, MaxUserTestNumber: t.MaxUserTestNumber,
		MinSubmissionIntervalS: t.MinSubmissionIntervalS, MinUserTestIntervalS: t.MinUserTestIntervalS,
		FeedbackLevel: t.FeedbackLevel, ScorePrecision: t.ScorePrecision, ScoreMode: t.ScoreMode,
	}
}

// NewDatasetParams returns dataset creation parameters: Batch task type, Sum
// score type, 1 s / 256 MiB limits.
func NewDatasetParams(taskID int64, description string) sqlc.CreateDatasetParams {
	tl, mem := int32(1000), int64(256<<20)
	return sqlc.CreateDatasetParams{
		TaskID: taskID, Description: description, TimeLimitMs: &tl, MemoryLimitBytes: &mem,
		OutputLimitBytes: 64 << 20, ProcessLimit: 1, TaskType: "Batch",
		TaskTypeParams: json.RawMessage(`{}`), ScoreType: "Sum", ScoreTypeParams: json.RawMessage(`{}`),
	}
}

// DatasetToUpdate converts a row into update parameters.
func DatasetToUpdate(d sqlc.Dataset) sqlc.UpdateDatasetParams {
	return sqlc.UpdateDatasetParams{
		ID: d.ID, Description: d.Description, Autojudge: d.Autojudge, TimeLimitMs: d.TimeLimitMs,
		WallTimeLimitMs: d.WallTimeLimitMs, MemoryLimitBytes: d.MemoryLimitBytes, OutputLimitBytes: d.OutputLimitBytes,
		ProcessLimit: d.ProcessLimit, SourceSizeLimitBytes: d.SourceSizeLimitBytes, TaskType: d.TaskType,
		TaskTypeParams: d.TaskTypeParams, ScoreType: d.ScoreType, ScoreTypeParams: d.ScoreTypeParams,
	}
}

// ParticipationToUpdate converts a row into update parameters.
func ParticipationToUpdate(p sqlc.Participation) sqlc.UpdateParticipationParams {
	return sqlc.UpdateParticipationParams{
		ID: p.ID, TeamID: p.TeamID, Ip: p.Ip, DelayTimeS: p.DelayTimeS, ExtraTimeS: p.ExtraTimeS,
		Hidden: p.Hidden, Unrestricted: p.Unrestricted, StartingTime: p.StartingTime,
	}
}
