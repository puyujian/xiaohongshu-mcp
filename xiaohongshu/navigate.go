package xiaohongshu

import (
	"context"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type NavigateAction struct {
	page *rod.Page
}

func NewNavigate(page *rod.Page) *NavigateAction {
	return &NavigateAction{page: page}
}

func (n *NavigateAction) ToExplorePage(ctx context.Context) (err error) {
	defer recoverRodPanicAsError(ctx, &err)

	page := n.page.Context(ctx)

	if err = navigateWithRetry(page, "https://www.xiaohongshu.com/explore", 3); err != nil {
		return err
	}
	if err = page.WaitLoad(); err != nil {
		return err
	}
	if _, err = page.Element(`div#app`); err != nil {
		return err
	}

	return nil
}

func (n *NavigateAction) ToProfilePage(ctx context.Context) (err error) {
	defer recoverRodPanicAsError(ctx, &err)

	page := n.page.Context(ctx)

	// First navigate to explore page
	if err := n.ToExplorePage(ctx); err != nil {
		return err
	}

	if err = page.WaitStable(time.Second); err != nil {
		return err
	}

	// Find and click the "我" channel link in sidebar
	profileLink, err := page.Element(`div.main-container li.user.side-bar-component a.link-wrapper span.channel`)
	if err != nil {
		return err
	}
	if err = profileLink.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}

	// Wait for navigation to complete
	if err = page.WaitLoad(); err != nil {
		return err
	}

	return nil
}

func (n *NavigateAction) ToNotificationPage(ctx context.Context) (err error) {
	defer recoverRodPanicAsError(ctx, &err)

	page := n.page.Context(ctx)

	if err := n.ToExplorePage(ctx); err != nil {
		return err
	}

	if err = page.WaitStable(time.Second); err != nil {
		return err
	}

	notificationLink, err := page.Element(`a.link-wrapper[href="/notification"]`)
	if err != nil {
		return err
	}
	if err = notificationLink.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	if err = page.Wait(rod.Eval(`() => location.pathname === "/notification"`)); err != nil {
		return err
	}

	return page.WaitStable(time.Second)
}

func (n *NavigateAction) ToNotificationMentionsPage(ctx context.Context) (err error) {
	defer recoverRodPanicAsError(ctx, &err)

	page := n.page.Context(ctx)

	if err := n.ToNotificationPage(ctx); err != nil {
		return err
	}

	if err = page.Wait(rod.Eval(`() => [...document.querySelectorAll('.reds-tab-item')].some(
		(el) => (el.textContent || '').trim() === '评论和@'
	)`)); err != nil {
		return err
	}

	if _, err = page.Eval(`() => {
		const mentionsTab = [...document.querySelectorAll('.reds-tab-item')].find(
			(el) => (el.textContent || '').trim() === '评论和@'
		);
		if (!mentionsTab) {
			return false;
		}
		if (!mentionsTab.classList.contains('active')) {
			mentionsTab.click();
		}
		return true;
	}`); err != nil {
		return err
	}

	if err = page.Wait(rod.Eval(`() => {
		const activeTab = document.querySelector('.reds-tab-item.active');
		return !!activeTab && (activeTab.textContent || '').trim() === '评论和@';
	}`)); err != nil {
		return err
	}

	return page.WaitStable(time.Second)
}
