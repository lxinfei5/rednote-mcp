package xiaohongshu

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// PublishVideoContent 发布视频内容
type PublishVideoContent struct {
	Title        string
	Content      string
	Tags         []string
	VideoPath    string
	ScheduleTime *time.Time // 定时发布时间，nil 表示立即发布
	Visibility   string     // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
	Products     []string   // 商品关键词列表，用于绑定带货商品
}

// NewPublishVideoAction 进入发布页并切换到"上传视频"
func NewPublishVideoAction(page *rod.Page) (*PublishAction, error) {
	pp := page.Timeout(300 * time.Second)

	if err := pp.Navigate(urlOfPublic); err != nil {
		return nil, errors.Wrap(err, "navigate to publish page failed")
	}

	// 使用 WaitLoad 代替 WaitIdle（更宽松）
	if err := pp.WaitLoad(); err != nil {
		logrus.Warnf("等待页面加载出现问题: %v，继续尝试", err)
	}
	time.Sleep(2 * time.Second)

	if err := pp.WaitDOMStable(time.Second, 0.1); err != nil {
		logrus.Warnf("等待 DOM 稳定出现问题: %v，继续尝试", err)
	}
	time.Sleep(1 * time.Second)

	if err := mustClickPublishTab(pp, "上传视频"); err != nil {
		return nil, errors.Wrap(err, "switch to upload video failed")
	}

	time.Sleep(1 * time.Second)

	return &PublishAction{page: pp}, nil
}

// PublishVideo 上传视频并提交
func (p *PublishAction) PublishVideo(ctx context.Context, content PublishVideoContent) error {
	if content.VideoPath == "" {
		return errors.New("video path cannot be empty")
	}

	page := p.page.Context(ctx)

	if err := uploadVideo(page, content.VideoPath); err != nil {
		return errors.Wrap(err, "upload video to xiaohongshu failed")
	}

	if err := submitPublishVideo(page, content.Title, content.Content, content.Tags, content.ScheduleTime, content.Visibility, content.Products); err != nil {
		return errors.Wrap(err, "publish to xiaohongshu failed")
	}
	return nil
}

// uploadVideo 上传单个本地视频
func uploadVideo(page *rod.Page, videoPath string) error {
	pp := page.Timeout(5 * time.Minute) // 视频处理耗时更长

	if _, err := os.Stat(videoPath); os.IsNotExist(err) {
		return errors.Wrapf(err, "video file not found: %s", videoPath)
	}

	// 寻找文件上传输入框（与图文一致的 class，或退回到 input[type=file]）
	var fileInput *rod.Element
	var err error
	fileInput, err = pp.Element(".upload-input")
	if err != nil || fileInput == nil {
		fileInput, err = pp.Element("input[type='file']")
		if err != nil || fileInput == nil {
			return errors.New("video upload input not found")
		}
	}

	fileInput.MustSetFiles(videoPath)

	// 对于视频，等待发布按钮变为可点击即表示处理完成
	btn, err := waitForPublishButtonClickable(pp, 10*time.Minute)
	if err != nil {
		return err
	}
	slog.Info("视频上传/处理完成，发布按钮可点击", "btn", btn)
	return nil
}

// submitPublishVideo 填写标题、正文、标签并点击发布（等待按钮可点击后再提交）
func submitPublishVideo(page *rod.Page, title, content string, tags []string, scheduleTime *time.Time, visibility string, products []string) error {
	// 标题
	titleElem, err := page.Element("div.d-input input")
	if err != nil {
		return errors.Wrap(err, "find title input failed")
	}
	if err := titleElem.Input(title); err != nil {
		return errors.Wrap(err, "input title failed")
	}
	time.Sleep(1 * time.Second)

	// 正文 + 标签
	contentElem, ok := getContentElement(page)
	if !ok {
		return errors.New("content input not found")
	}
	if err := contentElem.Input(content); err != nil {
		return errors.Wrap(err, "input content failed")
	}
	if err := waitAndClickTitleInput(titleElem); err != nil {
		return err
	}
	if err := inputTags(contentElem, tags); err != nil {
		return err
	}

	time.Sleep(1 * time.Second)

	// 处理定时发布
	if scheduleTime != nil {
		if err := setSchedulePublish(page, *scheduleTime); err != nil {
			return errors.Wrap(err, "set scheduled publish failed")
		}
		slog.Info("定时发布设置完成", "schedule_time", scheduleTime.Format("2006-01-02 15:04"))
	}

	// 设置可见范围
	if err := setVisibility(page, visibility); err != nil {
		return errors.Wrap(err, "set visibility failed")
	}

	// 绑定商品
	if err := bindProducts(page, products); err != nil {
		return errors.Wrap(err, "bind products failed")
	}

	if err := clickPublishButton(page); err != nil {
		return err
	}

	time.Sleep(3 * time.Second)
	return nil
}
