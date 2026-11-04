package ports

import "context"

// SCMReviewComment is one required change the reviewer anchored on a line of
// the reviewed commit.
type SCMReviewComment struct {
	Path string
	Line int
	Body string
}

// SCMReviewPost is a finished AO review that the daemon posts on the provider
// for the reviewer. TargetSHA is the commit the reviewer read, so line
// positions are placed against that commit and not a newer push.
type SCMReviewPost struct {
	PR        SCMPRRef
	TargetSHA string
	Summary   string
	Comments  []SCMReviewComment
}

// SCMReviewPoster posts an AO review on a pull request through an SCM
// provider. Providers whose reviewers post their own reviews (GitHub) return
// ErrSCMUnsupported.
type SCMReviewPoster interface {
	PostReview(ctx context.Context, post SCMReviewPost) error
}
