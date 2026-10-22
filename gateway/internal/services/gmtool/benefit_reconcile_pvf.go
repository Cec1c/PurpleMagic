package gmtool

import (
	"context"
	"strconv"

	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

func (s *Server) reconcileCampaignPVF(ctx context.Context, c testbenefits.Campaign) (testbenefits.Campaign, error) {
	if c.Reward.PVFChecksum == "" || c.Reward.PVFChecksum == s.v1PVFChecksum() {
		return c, nil
	}
	p, err := s.v1ReprojectCampaign(c)
	if err != nil {
		return c, err
	}
	if !testbenefits.UnchangedCampaignProjection(c, p) {
		return c, testbenefits.ErrPVFReview
	}
	command := testbenefits.PVFReviewCommand{CampaignID: c.ID, Revision: c.Revision, TargetChecksum: p.PVFChecksum, ProjectionHash: testbenefits.PVFProjectionHash(p)}
	_, err = s.deps.Benefits.ApplyCampaignPVF(ctx, "auto-pvf:"+c.ID+":"+strconv.FormatUint(c.Revision, 10)+":"+p.PVFChecksum,
		command, "自动校验：物品定义与发放参数一致", func(locked testbenefits.Campaign) (testbenefits.PVFProjection, error) {
			current, e := s.v1ReprojectCampaign(locked)
			if e != nil {
				return current, e
			}
			if !testbenefits.UnchangedCampaignProjection(locked, current) {
				return current, testbenefits.ErrPVFReview
			}
			return current, nil
		})
	if err != nil {
		return c, err
	}
	return s.deps.Benefits.Campaign(ctx, c.ID)
}
