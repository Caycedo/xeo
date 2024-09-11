package xeo

import "net/url"

// Game represents a game.
type Game struct {
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Photo        []PhotoSize     `json:"photo"`
	Text         string          `json:"text,omitempty"`
	TextEntities []MessageEntity `json:"text_entities,omitempty"`
	Animation    Animation       `json:"animation,omitempty"`
}

// CallbackGame is a placeholder, currently holds no information.
type CallbackGame struct{}

// GameHighScore represents one row of the high scores table for a game.
type GameHighScore struct {
	User     User `json:"user"`
	Position int  `json:"position"`
	Score    int  `json:"score"`
}

// GameScoreOptions contains the optional parameters used in SetGameScore method.
type GameScoreOptions struct {
	Force              bool `query:"force"`
	DisableEditMessage bool `query:"disable_edit_message"`
}

// SendGame is used to send a Game.
func (a *API) SendGame(gameShortName string, chatID int64, opts *BaseOptions) (res APIResponseMessage, err error) {
	var vals = make(url.Values)

	vals.Set("chat_id", itoa(chatID))
	vals.Set("game_short_name", gameShortName)
	return res, a.get("sendGame", addValues(vals, opts), &res)
}

// SetGameScore is used to set the score of the specified user in a game.
func (a *API) SetGameScore(userID int64, score int, msgID MessageIDOptions, opts *GameScoreOptions) (res APIResponseMessage, err error) {
	var vals = make(url.Values)

	vals.Set("user_id", itoa(userID))
	vals.Set("score", itoa(int64(score)))
	return res, a.get("setGameScore", addValues(addValues(vals, msgID), opts), &res)
}

// GetGameHighScores is used to get data for high score tables.
func (a *API) GetGameHighScores(userID int64, opts MessageIDOptions) (res APIResponseGameHighScore, err error) {
	var vals = make(url.Values)

	vals.Set("user_id", itoa(userID))
	return res, a.get("getGameHighScores", addValues(vals, opts), &res)
}
