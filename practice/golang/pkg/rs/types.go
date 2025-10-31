package rs

// MemberReadTask is the unit task payload.
type MemberReadTask struct {
	ID             string `json:"id"`
	MessageID      int64  `json:"message_id"`
	ChannelID      string `json:"channel_id"`
	ChannelType    uint8  `json:"channel_type"`
	UID            string `json:"uid"`
	MessageIDStr   string `json:"message_id_str"`
	MessageSeq     uint32 `json:"message_seq"`
	FromUID        string `json:"from_uid"`
	LoginUID       string `json:"login_uid"`
	ReqChannelID   string `json:"req_channel_id"`
	ReqChannelType uint8  `json:"req_channel_type"`
}

// BatchMemberReadTask is the batch container.
type BatchMemberReadTask struct {
	ID    string            `json:"id"`
	Tasks []*MemberReadTask `json:"tasks"`
}
