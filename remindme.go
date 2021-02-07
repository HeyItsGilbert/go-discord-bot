package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/honeycombio/beeline-go"
	"go.mongodb.org/mongo-driver/mongo"
)

type Reminder struct {
	Due             time.Time `json:"due" bson:"due"`
	Message         string    `json:"message" bson:"message"`
	Server          string    `json:"server" bson:"server"`
	Creator         string    `json:"creator" bson:"creator"`
	Channel         string    `json:"channel" bson:"channel"`
	SourceMessage   string    `json:"sourceMessage" bson:"sourceMessage"`
	SourceTimestamp time.Time `json:"sourceTimestamp" bson:"sourceTimestamp"`
}

func sendReminders(session *discordgo.Session) {
	storage := os.Getenv("COSMOSDB_URI")
	interval, err := strconv.Atoi(os.Getenv("REMINDER_INTERVAL"))
	if err != nil {
		interval = 5
	}
	for {
		time.Sleep(time.Duration(interval) * time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		ctx, span := beeline.StartSpan(ctx, "sendReminders")

		db, err := connectDb(ctx, storage)
		if err != nil {
			span.AddField("sendReminders.connect.error", err)
			span.Send()
			continue
		}

		reminders, err := findReminders(ctx, db, interval)
		if err != nil {
			span.AddField("sendReminders.find.error", err)
			span.Send()
			continue
		}

		for _, r := range reminders {
			ctx, childSpan := beeline.StartSpan(ctx, "sendReminderIndividual")
			childSpan.AddField("sendReminderIndividual.due", r.Due)
			childSpan.AddField("sendReminderIndividual.message", r.Message)
			childSpan.AddField("sendReminderIndividual.server", r.Server)
			childSpan.AddField("sendReminderIndividual.creator", r.Creator)
			childSpan.AddField("sendReminderIndividual.channel", r.Channel)
			childSpan.AddField("sendReminderIndividual.sourceMessage", r.SourceMessage)
			childSpan.AddField("sendReminderIndividual.sourceTimestamp", r.SourceTimestamp)

			message := fmt.Sprintf("Hey <@%s>, remember %s", r.Creator, r.Message)

			sendResponse(ctx, session, r.Channel, message)

			childSpan.Send()
		}

		span.Send()
	}
}

func findReminders(ctx context.Context, db *mongo.Client, interval int) ([]Reminder, error) {

	return nil, nil
}

func createReminder(ctx context.Context, message *discordgo.Message) (string, error) {

	ctx, span := beeline.StartSpan(ctx, "createReminder")
	defer span.Send()

	r, err := parseReminder(ctx, message)
	if err != nil {
		span.AddField("createReminder.error", err)
		return "", err
	}

	err = storeReminder(ctx, r)
	if err != nil {
		span.AddField("createReminder.error", err)
		return "", err
	}

	return "Reminder added.", nil
}

func parseReminder(ctx context.Context, message *discordgo.Message) (Reminder, error) {

	ctx, span := beeline.StartSpan(ctx, "parseReminder")
	defer span.Send()

	// expect message content to be like:
	// "do the thing 5h"
	// "5d wrankle the sprockets"
	// support minute, hour, day, Month

	timePattern := regexp.MustCompile("(?P<count>\\d+)(?P<interval>m|[hH]|[dD]|M)")
	names := timePattern.SubexpNames()
	elements := map[string]string{}
	matchingStrings := timePattern.FindAllStringSubmatch(message.Content, -1)
	matches := []string{}

	span.AddField("parseReminder.matcheslength", len(matchingStrings))
	span.AddField("parseReminder.matches", matchingStrings)

	if len(matchingStrings) == 0 {
		span.AddField("parseReminder.error", "No interval specified")
		return Reminder{}, fmt.Errorf("No interval specified")
	}

	matches = matchingStrings[0]

	for i, match := range matches {
		elements[names[i]] = match
	}

	span.AddField("parseReminder.count", elements["count"])
	span.AddField("parseReminder.interval", elements["interval"])

	reminderText := strings.Replace(message.Content, fmt.Sprintf("%s%s", elements["count"], elements["interval"]), "", 1)

	sourceDate, err := message.Timestamp.Parse()
	if err != nil {
		span.AddField("parseReminder.error", err)
		return Reminder{}, err
	}

	var dueDate time.Time
	timeCount, _ := strconv.Atoi(elements["count"])

	switch elements["interval"] {
	case "m":
		dueDate = sourceDate.Add(time.Duration(timeCount) * time.Minute)
	case "h", "H":
		dueDate = sourceDate.Add(time.Duration(timeCount) * time.Hour)
	case "d", "D":
		dueDate = sourceDate.AddDate(0, 0, timeCount)
	case "M":
		dueDate = sourceDate.AddDate(0, timeCount, 0)
	}

	r := Reminder{
		Due:             dueDate,
		Message:         reminderText,
		Server:          message.GuildID,
		Creator:         message.Author.ID,
		Channel:         message.ChannelID,
		SourceMessage:   message.ID,
		SourceTimestamp: sourceDate,
	}

	span.AddField("parseReminder.due", r.Due)
	span.AddField("parseReminder.message", r.Message)
	span.AddField("parseReminder.server", r.Server)
	span.AddField("parseReminder.creator", r.Creator)
	span.AddField("parseReminder.channel", r.Channel)
	span.AddField("parseReminder.sourceMessage", r.SourceMessage)
	span.AddField("parseReminder.sourceTimestamp", r.SourceTimestamp)

	span.AddField("parseReminder.reminder", r)
	return r, nil
}

func storeReminder(ctx context.Context, r Reminder) error {

	ctx, span := beeline.StartSpan(ctx, "storeReminder")
	defer span.Send()
	span.AddField("storeReminder.reminder", r)

	db, err := connectDb(ctx, os.Getenv("COSMOSDB_URI"))
	if err != nil {
		span.AddField("storeReminder.error", err)
		return err
	}

	err = writeDbObject(ctx, db, r)
	if err != nil {
		span.AddField("storeReminder.error", err)
		return err
	}

	if err = db.Disconnect(ctx); err != nil {
		span.AddField("storeReminder.error", err)
		return err
	}

	return nil
}
