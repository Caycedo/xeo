package xeo

// ChatAdministratorRights represents the rights of an administrator in a chat.
type ChatAdministratorRights struct {
	IsAnonymous          bool `json:"is_anonymous"`
	CanManageChat        bool `json:"can_manage_chat"`
	CanDeleteMessages    bool `json:"can_delete_messages"`
	CanManageVideo_chats bool `json:"can_manage_video_chats"`
	CanRestrictMembers   bool `json:"can_restrict_members"`
	CanPromoteMembers    bool `json:"can_promote_members"`
	CanChangeInfo        bool `json:"can_change_info"`
	CanInviteUsers       bool `json:"can_invite_users"`
	CanPostMessages      bool `json:"can_post_messages,omitempty"`
	CanEditMessages      bool `json:"can_edit_messages,omitempty"`
	CanPinMessages       bool `json:"can_pin_messages,omitempty"`
	CanPostStories       bool `json:"can_post_stories,omitempty"`
	CanEditStories       bool `json:"can_edit_stories,omitempty"`
	CanDeleteStories     bool `json:"can_delete_stories,omitempty"`
	CanManageTopics      bool `json:"can_manage_topics,omitempty"`
}

// SetMyDefaultAdministratorRightsOptions contains the optional parameters used by
// the SetMyDefaultAdministratorRights method.
type SetMyDefaultAdministratorRightsOptions struct {
	Rights      ChatAdministratorRights `query:"rights"`
	ForChannels bool                    `query:"for_channels"`
}

// GetMyDefaultAdministratorRightsOptions contains the optional parameters used by
// the GetMyDefaultAdministratorRights method.
type GetMyDefaultAdministratorRightsOptions struct {
	ForChannels bool `query:"for_channels"`
}

// SetMyDefaultAdministratorRights is used to change the default administrator rights
// requested by the bot when it's added as an administrator to groups or channels.
// These rights will be suggested to users, but they are are free to modify the list
// before adding the bot.
func (a *API) SetMyDefaultAdministratorRights(opts *SetMyDefaultAdministratorRightsOptions) (res APIResponseBool, err error) {
	return res, a.get("setMyDefaultAdministratorRights", urlValues(opts), &res)
}

// GetMyDefaultAdministratorRights is used to get the current default administrator rights of the bot.
func (a *API) GetMyDefaultAdministratorRights(opts *GetMyDefaultAdministratorRightsOptions) (res APIResponseChatAdministratorRights, err error) {
	return res, a.get("getMyDefaultAdministratorRights", urlValues(opts), &res)
}
