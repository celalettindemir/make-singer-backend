package credit

import "time"

// CampaignView is a campaign as the admin API shows it.
type CampaignView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Quota     int        `json:"quota"`
	PerUser   int        `json:"perUser"`
	ValidDays int        `json:"validDays"`
	EndsAt    *string    `json:"endsAt"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"startedAt"`
	StoppedAt *time.Time `json:"stoppedAt"`
	CreatedBy string     `json:"createdBy"`
	Joined    int        `json:"joined"`
}

// View is the API form of the campaign.
func (c Campaign) View() CampaignView {
	v := CampaignView{
		ID: CampaignUUID(c.ID), Name: c.Name, Quota: c.Quota, PerUser: c.PerUser, ValidDays: c.ValidDays,
		Status: c.Status.JSON(), StartedAt: c.StartedAt.UTC(), CreatedBy: c.CreatedBy, Joined: c.Joined,
	}
	if c.EndsAt != nil {
		day := c.EndsAt.UTC().Format(time.DateOnly)
		v.EndsAt = &day
	}
	if c.StoppedAt != nil {
		t := c.StoppedAt.UTC()
		v.StoppedAt = &t
	}
	return v
}

// CampaignListView is the answer of the campaign list.
type CampaignListView struct {
	Summary CampaignSummaryView `json:"summary"`
	Items   []CampaignView      `json:"items"`
}

// CampaignSummaryView holds the figures above the campaign list.
type CampaignSummaryView struct {
	Active               *CampaignView        `json:"active"`
	DistributedThisMonth DistributedMonthView `json:"distributedThisMonth"`
	RemainingQuota       *RemainingQuotaView  `json:"remainingQuota"`
}

// DistributedMonthView is what campaigns gave since the first of the month.
type DistributedMonthView struct {
	Credits int `json:"credits"`
	Users   int `json:"users"`
}

// RemainingQuotaView is what the active campaign can still give.
type RemainingQuotaView struct {
	People  int `json:"people"`
	Credits int `json:"credits"`
}

// View is the API form of the overview.
func (o CampaignOverview) View() CampaignListView {
	v := CampaignListView{
		Items:   make([]CampaignView, 0, len(o.Campaigns)),
		Summary: CampaignSummaryView{DistributedThisMonth: DistributedMonthView{Credits: o.MonthCredits, Users: o.MonthPeople}},
	}
	for _, c := range o.Campaigns {
		v.Items = append(v.Items, c.View())
	}
	if o.Active != nil {
		a := o.Active.View()
		v.Summary.Active = &a
		v.Summary.RemainingQuota = &RemainingQuotaView{People: o.QuotaLeft, Credits: o.QuotaLeft * o.Active.PerUser}
	}
	return v
}

// CampaignDetailView is the answer of the campaign detail.
type CampaignDetailView struct {
	Campaign    CampaignView `json:"campaign"`
	Distributed int          `json:"distributed"`
	Used        int          `json:"used"`
	UsedPercent int          `json:"usedPercent"`
	RecentJoins []JoinView   `json:"recentJoins"`
}

// JoinView is one person who got the gift.
type JoinView struct {
	UserID   string    `json:"userId"`
	ShortID  string    `json:"shortId"`
	Amount   int       `json:"amount"`
	JoinedAt time.Time `json:"joinedAt"`
}

// View is the API form of the detail.
func (d CampaignDetail) View() CampaignDetailView {
	v := CampaignDetailView{
		Campaign: d.Campaign.View(), Distributed: d.Distributed, Used: d.Used, UsedPercent: d.UsedPercent,
		RecentJoins: make([]JoinView, 0, len(d.Recent)),
	}
	for _, p := range d.Recent {
		v.RecentJoins = append(v.RecentJoins, JoinView{
			UserID: p.UserID, ShortID: ShortID(p.UserID), Amount: d.PerUser, JoinedAt: p.JoinedAt.UTC(),
		})
	}
	return v
}

// CreatedCampaignView is the answer of createCampaign.
type CreatedCampaignView struct {
	Campaign CampaignView  `json:"campaign"`
	Replaced *CampaignView `json:"replaced"`
}
