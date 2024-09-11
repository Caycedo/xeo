package xeo

// MenuButtonType is a custom type for the various MenuButton*'s Type field.
type MenuButtonType string

// These are all the possible types for the various MenuButton*'s Type field.
const (
	MenuButtonTypeCommands MenuButtonType = "commands"
	MenuButtonTypeWebApp                  = "web_app"
	MenuButtonTypeDefault                 = "default"
)

// MenuButton is a unique type for MenuButtonCommands, MenuButtonWebApp and MenuButtonDefault
type MenuButton struct {
	WebApp *WebAppInfo    `json:"web_app,omitempty"`
	Type   MenuButtonType `json:"type"`
	Text   string         `json:"text,omitempty"`
}

// SetChatMenuButtonOptions contains the optional parameters used by the SetChatMenuButton method.
type SetChatMenuButtonOptions struct {
	MenuButton MenuButton `query:"menu_button"`
	ChatID     int64      `query:"chat_id"`
}

// GetChatMenuButtonOptions contains the optional parameters used by the GetChatMenuButton method.
type GetChatMenuButtonOptions struct {
	ChatID int64 `query:"chat_id"`
}

// SetChatMenuButton is used to change the bot's menu button in a private chat, or the default menu button.
func (a *API) SetChatMenuButton(opts *SetChatMenuButtonOptions) (res APIResponseBool, err error) {
	return res, a.get("setChatMenuButton", urlValues(opts), &res)
}

// GetChatMenuButton is used to get the current value of the bot's menu button in a private chat, or the default menu button.
func (a *API) GetChatMenuButton(opts *GetChatMenuButtonOptions) (res APIResponseMenuButton, err error) {
	return res, a.get("getChatMenuButton", urlValues(opts), &res)
}
