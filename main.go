package main

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/spf13/viper"
	"github.com/zmb3/spotify"
	"golang.org/x/oauth2/clientcredentials"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	server = make(map[string]*sync.Mutex)
	skip   = make(map[string]bool)
	queue  = make(map[string][]Queue)
	vc     = make(map[string]*discordgo.VoiceConnection)
	client spotify.Client
	Token  string
	Prefix string
)

func init() {

	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	viper.AddConfigPath(".")

	viper.SetDefault("prefix", "!")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found
			fmt.Println("Config file not found! See example_config.yml")
			return
		}
	} else {
		//Config file found
		Token = viper.GetString("token")
		Prefix = viper.GetString("prefix")

		//Spotify credentials
		config := &clientcredentials.Config{
			ClientID:     viper.GetString("clientid"),
			ClientSecret: viper.GetString("clientsecret"),
			TokenURL:     spotify.TokenURL,
		}

		token, err := config.Token(context.Background())
		if err != nil {
			log.Fatalf("couldn't get token: %v", err)
			return
		}

		client = spotify.Authenticator{}.NewClient(token)

	}
}

func main() {

	if Token == "" {
		fmt.Println("No Token provided. Please modify config.yml")
		return
	}

	if Prefix == "" {
		fmt.Println("No Prefix provided. Please modify config.yml")
		return
	}

	// Create a new Discord session using the provided bot Token.
	dg, err := discordgo.New("Bot " + Token)
	if err != nil {
		fmt.Println("Error creating Discord session: ", err)
		return
	}

	dg.AddHandler(messageCreate)
	dg.AddHandler(guildCreate)

	//Initialize intents that we use
	dg.Identify.Intents = discordgo.MakeIntent(discordgo.IntentsGuildMessages | discordgo.IntentsGuilds | discordgo.IntentsGuildVoiceStates)

	// Open the websocket and begin listening.
	err = dg.Open()
	if err != nil {
		fmt.Println("Error opening Discord session: ", err)
	}

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("discordMusicBot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close down the Discord session.
	_ = dg.Close()
}

//Initialize for every guild mutex and skip variable
func guildCreate(_ *discordgo.Session, event *discordgo.GuildCreate) {
	server[event.ID] = &sync.Mutex{}
	skip[event.ID] = true
}

// This function will be called (due to AddHandler above) every time a new
// message is created on any channel that the autenticated bot has access to.
func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if s.State.User.ID == m.Author.ID {
		return
	}

	switch strings.Split(strings.ToLower(m.Content), " ")[0] {
		//Plays a song
	case Prefix + "play", Prefix + "p":
		go deleteMessage(s, m)

		link := strings.TrimPrefix(m.Content, Prefix+"play ")
		link = strings.TrimPrefix(link, Prefix+"p ")

		if isValidUrl(link) {
			downloadAndPlay(s, m.GuildID, findUserVoiceState(s, m), link, m.Author.Username, m.ChannelID)
		} else {
			if strings.HasPrefix(link, "spotify:playlist:") {
				spotifyPlaylist(s, m.GuildID, findUserVoiceState(s, m), m.Author.Username, strings.TrimPrefix(m.Content, Prefix+"spotify "), m.ChannelID)
			} else {
				searchDownloadAndPlay(s, m.GuildID, findUserVoiceState(s, m), link, m.Author.Username, m.ChannelID)
			}
		}
		break

		//Skips a song
	case Prefix + "skip", Prefix + "s":
		go deleteMessage(s, m)
		skip[m.GuildID] = false
		break

		//Clear the queue of the guild
	case Prefix + "clear", Prefix + "c":
		go deleteMessage(s, m)
		//TODO: Clear queue logic
		break

		//Prints out queue for the guild
	case Prefix + "queue", Prefix + "q":
		go deleteMessage(s, m)
		var message string

		//Generate song info for message
		for i, el := range queue[m.GuildID] {
			if i == 0 {
				if el.title != "" {
					message += "Currently playing: " + el.title + " - " + el.duration + " added by " + el.user + "\n\n"
					continue
				} else {
					message += "Currently playing: Getting info...\n\n"
					continue
				}

			}
			//If we don't have the title, we use some placeholder text
			if el.title == "" {
				message += strconv.Itoa(i) + ") Getting info...\n"
			} else {
				message += strconv.Itoa(i) + ") " + el.title + " - " + el.duration + " by " + el.user + "\n"
			}

		}

		//Send embed
		em, err := s.ChannelMessageSendEmbed(m.ChannelID, NewEmbed().SetTitle(s.State.User.Username).AddField("Queue", message).SetColor(0x7289DA).MessageEmbed)
		if err != nil {
			fmt.Println("Error sending queue embed: ", err)
			return
		}

		//Wait for 15 seconds, then delete the message
		time.Sleep(time.Second * 15)
		err = s.ChannelMessageDelete(m.ChannelID, em.ID)
		if err != nil {
			fmt.Println("Error deleting queue embed: ", err)
		}
		break

		//Disconnect the bot from the guild voice channel
	case Prefix + "disconnect", Prefix + "d":
		go deleteMessage(s, m)
		_ = vc[m.GuildID].Disconnect()
		vc[m.GuildID] = nil
		break

		//We summon the bot in the user current voice channel
	case Prefix + "summon":
		go deleteMessage(s, m)
		var err error
		vc[m.GuildID], err = s.ChannelVoiceJoin(m.GuildID, findUserVoiceState(s, m), false, false)
		if err != nil {
			fmt.Println(err)
		}
		break

		//Prints out supported commands
	case Prefix + "help", Prefix + "h":
		go deleteMessage(s, m)
		mex, err := s.ChannelMessageSend(m.ChannelID, "Supported commands:\n```"+Prefix+"play <link> - Plays a song from youtube or spotify playlist\n"+Prefix+"queue - Returns all the songs in the server queue\n"+Prefix+"summon - Make the bot join your voice channel\n"+Prefix+"disconnect - Disconnect the bot from the voice channel```")
		if err != nil {
			fmt.Println(err)
			break
		}

		time.Sleep(time.Second * 30)

		err = s.ChannelMessageDelete(m.ChannelID, mex.ID)
		if err != nil {
			fmt.Println(err)
		}
		break
	}

}
