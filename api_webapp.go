package xeo

import (
	"encoding/json"
	"net/url"
)

// WebAppInfo contains information about a Web App.
type WebAppInfo struct {
	URL string `json:"url"`
}

// SentWebAppMessage contains information about an inline message sent
// by a Web App on behalf of a user.
type SentWebAppMessage struct {
	InlineMessageID string `json:"inline_message_id,omitempty"`
}

// WebAppData contains data sent from a Web App to the bot.
type WebAppData struct {
	Data       string `json:"data"`
	ButtonText string `json:"button_text"`
}

// AnswerWebAppQuery is used to set the result of an interaction with a Web App
// and send a corresponding message on behalf of the user to the chat from which
// the query originated.
func (a *API) AnswerWebAppQuery(webAppQueryID string, result InlineQueryResult) (res APIResponseSentWebAppMessage, err error) {
	var vals = make(url.Values)

	resultJson, err := json.Marshal(result)
	if err != nil {
		return res, err
	}

	vals.Set("web_app_query_id", webAppQueryID)
	vals.Set("result", string(resultJson))
	return res, a.get("answerWebAppQuery", vals, &res)
}
