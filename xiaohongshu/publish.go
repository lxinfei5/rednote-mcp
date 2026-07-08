package xiaohongshu

import (
	"context"
	"log/slog"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"github.com/h2non/filetype"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// PublishImageContent 发布图文内容
type PublishImageContent struct {
	Title        string
	Content      string
	Tags         []string
	ImagePaths   []string
	ScheduleTime *time.Time // 定时发布时间，nil 表示立即发布
	IsOriginal   bool       // 是否声明原创
	Visibility   string     // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
	Products     []string   // 商品关键词列表，用于绑定带货商品
}

type PublishAction struct {
	page *rod.Page
}

const (
	urlOfPublic = `https://creator.xiaohongshu.com/publish/publish?source=official`
)

func NewPublishImageAction(page *rod.Page) (*PublishAction, error) {

	pp := page.Timeout(300 * time.Second)

	// 使用更稳健的导航和等待策略
	if err := pp.Navigate(urlOfPublic); err != nil {
		return nil, errors.Wrap(err, "navigate to publish page failed")
	}

	// 等待页面加载，使用 WaitLoad 代替 WaitIdle（更宽松）
	if err := pp.WaitLoad(); err != nil {
		logrus.Warnf("等待页面加载出现问题: %v，继续尝试", err)
	}
	time.Sleep(2 * time.Second)

	// 等待页面稳定
	if err := pp.WaitDOMStable(time.Second, 0.1); err != nil {
		logrus.Warnf("等待 DOM 稳定出现问题: %v，继续尝试", err)
	}
	time.Sleep(1 * time.Second)

	if err := mustClickPublishTab(pp, "上传图文"); err != nil {
		logrus.Errorf("点击上传图文 TAB 失败: %v", err)
		return nil, err
	}

	time.Sleep(1 * time.Second)

	return &PublishAction{
		page: pp,
	}, nil
}

func (p *PublishAction) Publish(ctx context.Context, content PublishImageContent) error {
	if len(content.ImagePaths) == 0 {
		return errors.New("image paths cannot be empty")
	}

	page := p.page.Context(ctx)

	if err := uploadImages(page, content.ImagePaths); err != nil {
		return errors.Wrap(err, "upload images to xiaohongshu failed")
	}

	tags := content.Tags
	if len(tags) >= 10 {
		logrus.Warnf("标签数量超过10，截取前10个标签")
		tags = tags[:10]
	}

	logrus.Infof("发布内容: title=%s, images=%v, tags=%v, schedule=%v, original=%v, visibility=%s, products=%v", content.Title, len(content.ImagePaths), tags, content.ScheduleTime, content.IsOriginal, content.Visibility, content.Products)

	if err := submitPublish(page, content.Title, content.Content, tags, content.ScheduleTime, content.IsOriginal, content.Visibility, content.Products); err != nil {
		return errors.Wrap(err, "publish to xiaohongshu failed")
	}

	return nil
}

func removePopCover(page *rod.Page) {

	// 先移除弹窗封面
	has, elem, err := page.Has("div.d-popover")
	if err != nil {
		return
	}
	if has {
		elem.MustRemove()
	}

	// 兜底：点击一下空位置吧
	clickEmptyPosition(page)
}

func clickEmptyPosition(page *rod.Page) {
	x := 380 + rand.Intn(100)
	y := 20 + rand.Intn(60)
	page.Mouse.MustMoveTo(float64(x), float64(y)).MustClick(proto.InputMouseButtonLeft)
}

func mustClickPublishTab(page *rod.Page, tabname string) error {
	page.MustElement(`div.upload-content`).MustWaitVisible()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		tab, blocked, err := getTabElement(page, tabname)
		if err != nil {
			logrus.Warnf("获取发布 TAB 元素失败: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if tab == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if blocked {
			logrus.Info("发布 TAB 被遮挡，尝试移除遮挡")
			removePopCover(page)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if err := tab.Click(proto.InputMouseButtonLeft, 1); err != nil {
			logrus.Warnf("点击发布 TAB 失败: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		return nil
	}

	return errors.Errorf("publish tab not found - %s", tabname)
}

func getTabElement(page *rod.Page, tabname string) (*rod.Element, bool, error) {
	elems, err := page.Elements("div.creator-tab")
	if err != nil {
		return nil, false, err
	}

	for _, elem := range elems {
		if !isElementVisible(elem) {
			continue
		}

		text, err := elem.Text()
		if err != nil {
			logrus.Debugf("获取发布 TAB 文本失败: %v", err)
			continue
		}

		if strings.TrimSpace(text) != tabname {
			continue
		}

		blocked, err := isElementBlocked(elem)
		if err != nil {
			return nil, false, err
		}

		return elem, blocked, nil
	}

	return nil, false, nil
}

func isElementBlocked(elem *rod.Element) (bool, error) {
	// 用 go-rod 原生可交互性判断替代 JS 命中测试：被遮挡/不可见/pointer-events:none
	// 都会返回 NotInteractableError，语义等价于原先的 elementFromPoint 检测。
	if _, err := elem.Interactable(); err != nil {
		var notInteractable *rod.NotInteractableError
		if errors.As(err, &notInteractable) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func uploadImages(page *rod.Page, imagesPaths []string) error {
	// 验证文件路径有效性
	validPaths := make([]string, 0, len(imagesPaths))
	for _, path := range imagesPaths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			logrus.Warnf("图片文件不存在: %s", path)
			continue
		}
		// 上传前校验确为图片，避免把任意本地文件（如密钥/配置）提交到小红书
		if !isImageFile(path) {
			logrus.Warnf("跳过非图片文件: %s", path)
			continue
		}
		validPaths = append(validPaths, path)
		logrus.Infof("获取有效图片：%s", path)
	}

	// 逐张上传：每张上传后等待预览出现，再上传下一张
	for i, path := range validPaths {
		selector := `input[type="file"]`
		if i == 0 {
			selector = ".upload-input"
		}

		uploadInput, err := page.Element(selector)
		if err != nil {
			return errors.Wrapf(err, "find upload input failed (image %d)", i+1)
		}
		if err := uploadInput.SetFiles([]string{path}); err != nil {
			return errors.Wrapf(err, "upload image %d failed", i+1)
		}

		slog.Info("图片已提交上传", "index", i+1, "path", path)

		// 等待当前图片上传完成（预览元素数量达到 i+1），最多等 60 秒
		if err := waitForUploadComplete(page, i+1); err != nil {
			return errors.Wrapf(err, "image %d upload timed out", i+1)
		}
		time.Sleep(1 * time.Second)
	}

	return nil
}

// isImageFile 通过文件头判断是否为图片，仅读取头部字节，避免加载大文件。
func isImageFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	head := make([]byte, 262) // filetype 仅需文件头即可识别类型
	n, _ := f.Read(head)
	return filetype.IsImage(head[:n])
}

// waitForUploadComplete 等待第 expectedCount 张图片上传完成，最多等 60 秒
func waitForUploadComplete(page *rod.Page, expectedCount int) error {
	maxWaitTime := 60 * time.Second
	checkInterval := 500 * time.Millisecond
	start := time.Now()
	lastLogCount := expectedCount - 1

	for time.Since(start) < maxWaitTime {
		uploadedImages, err := page.Elements(".img-preview-area .pr")
		if err != nil {
			time.Sleep(checkInterval)
			continue
		}

		currentCount := len(uploadedImages)
		// 数量变化时才打印，避免刷屏
		if currentCount != lastLogCount {
			slog.Info("等待图片上传", "current", currentCount, "expected", expectedCount)
			lastLogCount = currentCount
		}
		if currentCount >= expectedCount {
			slog.Info("图片上传完成", "count", currentCount)
			return nil
		}

		time.Sleep(checkInterval)
	}

	return errors.Errorf("image %d upload timed out (60s), check network connection and image size", expectedCount)
}

func submitPublish(page *rod.Page, title, content string, tags []string, scheduleTime *time.Time, isOriginal bool, visibility string, products []string) error {
	titleElem, err := page.Element("div.d-input input")
	if err != nil {
		return errors.Wrap(err, "find title input failed")
	}
	if err := titleElem.Input(title); err != nil {
		return errors.Wrap(err, "input title failed")
	}

	// 检查标题长度
	time.Sleep(500 * time.Millisecond)
	if err := checkTitleMaxLength(page); err != nil {
		return err
	}
	slog.Info("检查标题长度：通过")

	time.Sleep(1 * time.Second)

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

	// 检查正文长度
	if err := checkContentMaxLength(page); err != nil {
		return err
	}
	slog.Info("检查正文长度：通过")

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

	// 处理原创声明
	if isOriginal {
		if err := setOriginal(page); err != nil {
			slog.Warn("设置原创声明失败，继续发布", "error", err)
		} else {
			slog.Info("已声明原创")
		}
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

type publishButton struct {
	elem     *rod.Element
	isWidget bool
}

func clickPublishButton(page *rod.Page) error {
	btn, err := waitForPublishButtonClickable(page, 15*time.Second)
	if err != nil {
		return err
	}

	if btn.isWidget {
		return clickPublishWidget(page, btn.elem)
	}

	if err := btn.elem.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "click publish button failed")
	}
	return nil
}

// waitForPublishButtonClickable 等待新版 xhs-publish-btn 或旧版 button.bg-red 可点击。
func waitForPublishButtonClickable(page *rod.Page, maxWait time.Duration) (*publishButton, error) {
	interval := 1 * time.Second
	start := time.Now()
	var lastDisabledReason string

	slog.Info("开始等待发布按钮可点击")

	for time.Since(start) < maxWait {
		btn, disabledReason, err := findPublishButton(page)
		if err != nil {
			slog.Warn("查找发布按钮失败，继续等待", "error", err)
			time.Sleep(interval)
			continue
		}
		if btn != nil && disabledReason == "" {
			return btn, nil
		}
		if disabledReason != "" {
			lastDisabledReason = disabledReason
		}
		time.Sleep(interval)
	}

	if lastDisabledReason != "" {
		return nil, errors.Errorf("timed out waiting for publish button to become clickable: %s", lastDisabledReason)
	}
	return nil, errors.New("timed out waiting for publish button to become clickable")
}

func findPublishButton(page *rod.Page) (*publishButton, string, error) {
	widgets, err := page.Elements("xhs-publish-btn")
	if err != nil {
		return nil, "", errors.Wrap(err, "find new publish button failed")
	}

	for _, widget := range widgets {
		if !isElementVisible(widget) {
			continue
		}

		isPublish, err := widget.Attribute("is-publish")
		if err != nil {
			return nil, "", errors.Wrap(err, "read new publish button is-publish attribute failed")
		}
		if isPublish != nil && *isPublish == "false" {
			continue
		}

		submitDisabled, err := widget.Attribute("submit-disabled")
		if err != nil {
			return nil, "", errors.Wrap(err, "read new publish button submit-disabled attribute failed")
		}
		if submitDisabled != nil && *submitDisabled == "true" {
			return &publishButton{elem: widget, isWidget: true}, "new publish button not clickable", nil
		}

		return &publishButton{elem: widget, isWidget: true}, "", nil
	}

	oldButtons, err := page.Elements(".publish-page-publish-btn button.bg-red")
	if err != nil {
		return nil, "", errors.Wrap(err, "find old publish button failed")
	}

	for _, oldButton := range oldButtons {
		if !isElementVisible(oldButton) {
			continue
		}

		if disabled, err := oldButton.Attribute("disabled"); err != nil {
			return nil, "", errors.Wrap(err, "read old publish button disabled attribute failed")
		} else if disabled != nil {
			return &publishButton{elem: oldButton}, "old publish button disabled", nil
		}

		if ariaDisabled, err := oldButton.Attribute("aria-disabled"); err != nil {
			return nil, "", errors.Wrap(err, "read old publish button aria-disabled attribute failed")
		} else if ariaDisabled != nil && *ariaDisabled == "true" {
			return &publishButton{elem: oldButton}, "old publish button aria-disabled=true", nil
		}

		if cls, err := oldButton.Attribute("class"); err != nil {
			return nil, "", errors.Wrap(err, "read old publish button class attribute failed")
		} else if cls != nil && hasExactClass(*cls, "disabled") {
			return &publishButton{elem: oldButton}, "old publish button has disabled class", nil
		}

		return &publishButton{elem: oldButton}, "", nil
	}

	return nil, "", nil
}

func clickPublishWidget(page *rod.Page, widget *rod.Element) error {
	if err := widget.ScrollIntoView(); err != nil {
		return errors.Wrap(err, "scroll new publish button into view failed")
	}
	time.Sleep(200 * time.Millisecond)

	shape, err := widget.Shape()
	if err != nil {
		return errors.Wrap(err, "get new publish button position failed")
	}
	if len(shape.Quads) == 0 {
		return errors.New("get new publish button position failed: no clickable area")
	}

	quad := shape.Quads[0]
	minX, maxX := quad[0], quad[0]
	minY, maxY := quad[1], quad[1]
	for i := 0; i < quad.Len(); i++ {
		x := quad[i*2]
		y := quad[i*2+1]
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}

	x := minX + (maxX-minX)*0.65
	y := minY + (maxY-minY)/2
	if err := page.Mouse.MoveTo(proto.Point{X: x, Y: y}); err != nil {
		return errors.Wrap(err, "move to new publish button failed")
	}
	if err := page.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "click publish button failed")
	}
	return nil
}

// waitAndClickTitleInput 在填写正文后等待 1 秒并回点标题输入框，增强后续交互稳定性
func waitAndClickTitleInput(titleElem *rod.Element) error {
	slog.Info("正文填写完成，准备等待后回点标题输入框")
	time.Sleep(1 * time.Second)
	if err := titleElem.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "click back on title input failed")
	}
	slog.Info("已回点标题输入框，继续后续发布流程")
	return nil
}

// 检查标题是否超过最大长度
func checkTitleMaxLength(page *rod.Page) error {
	has, elem, err := page.Has(`div.title-container div.max_suffix`)
	if err != nil {
		return errors.Wrap(err, "check title length element failed")
	}

	// 元素不存在，说明标题没超长
	if !has {
		return nil
	}

	// 元素存在，说明标题超长
	titleLength, err := elem.Text()
	if err != nil {
		return errors.Wrap(err, "get title length text failed")
	}

	return makeMaxLengthError(titleLength)
}

func checkContentMaxLength(page *rod.Page) error {
	has, elem, err := page.Has(`div.edit-container div.length-error`)
	if err != nil {
		return errors.Wrap(err, "check content length element failed")
	}

	// 元素不存在，说明正文没超长
	if !has {
		return nil
	}

	// 元素存在，说明正文超长
	contentLength, err := elem.Text()
	if err != nil {
		return errors.Wrap(err, "get content length text failed")
	}

	return makeMaxLengthError(contentLength)
}

func makeMaxLengthError(elemText string) error {
	parts := strings.Split(elemText, "/")
	if len(parts) != 2 {
		return errors.Errorf("length exceeds limit: %s", elemText)
	}

	currLen, maxLen := parts[0], parts[1]

	return errors.Errorf("current length %s, max length %s", currLen, maxLen)
}

// 查找内容输入框 - 使用Race方法处理两种样式
func getContentElement(page *rod.Page) (*rod.Element, bool) {
	var foundElement *rod.Element
	var found bool

	page.Race().
		Element("div.ql-editor").MustHandle(func(e *rod.Element) {
		foundElement = e
		found = true
	}).
		ElementFunc(func(page *rod.Page) (*rod.Element, error) {
			return findTextboxByPlaceholder(page)
		}).MustHandle(func(e *rod.Element) {
		foundElement = e
		found = true
	}).
		MustDo()

	if found {
		return foundElement, true
	}

	slog.Warn("no content element found by any method")
	return nil, false
}

func inputTags(contentElem *rod.Element, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	time.Sleep(1 * time.Second)

	for i := 0; i < 20; i++ {
		ka, err := contentElem.KeyActions()
		if err != nil {
			return errors.Wrap(err, "create keyboard action failed")
		}
		if err := ka.Type(input.ArrowDown).Do(); err != nil {
			return errors.Wrap(err, "press arrow key failed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ka, err := contentElem.KeyActions()
	if err != nil {
		return errors.Wrap(err, "create keyboard action failed")
	}
	if err := ka.Press(input.Enter).Press(input.Enter).Do(); err != nil {
		return errors.Wrap(err, "press enter key failed")
	}

	time.Sleep(1 * time.Second)

	for _, tag := range tags {
		tag = strings.TrimLeft(tag, "#")
		if err := inputTag(contentElem, tag); err != nil {
			return errors.Wrapf(err, "input tag [%s] failed", tag)
		}
	}
	return nil
}

func inputTag(contentElem *rod.Element, tag string) error {
	if err := contentElem.Input("#"); err != nil {
		return errors.Wrap(err, "input # failed")
	}
	sleepRandom(150, 350)

	for _, char := range tag {
		if err := contentElem.Input(string(char)); err != nil {
			return errors.Wrapf(err, "input char [%c] failed", char)
		}
		// 逐字符输入间隔加抖动，避免固定 50ms 的机械节奏
		sleepRandom(60, 180)
	}

	time.Sleep(1 * time.Second)

	page := contentElem.Page()
	topicContainer, err := page.Element("#creator-editor-topic-container")
	if err != nil || topicContainer == nil {
		slog.Warn("未找到标签联想下拉框，直接输入空格", "tag", tag)
		return contentElem.Input(" ")
	}

	firstItem, err := topicContainer.Element(".item")
	if err != nil || firstItem == nil {
		slog.Warn("未找到标签联想选项，直接输入空格", "tag", tag)
		return contentElem.Input(" ")
	}

	if err := firstItem.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "click tag suggestion option failed")
	}
	slog.Info("成功点击标签联想选项", "tag", tag)
	time.Sleep(200 * time.Millisecond)

	time.Sleep(500 * time.Millisecond) // 等待标签处理完成
	return nil
}

func findTextboxByPlaceholder(page *rod.Page) (*rod.Element, error) {
	elements := page.MustElements("p")
	if elements == nil {
		return nil, errors.New("no p elements found")
	}

	// 查找包含指定placeholder的元素
	placeholderElem := findPlaceholderElement(elements, "输入正文描述")
	if placeholderElem == nil {
		return nil, errors.New("no placeholder element found")
	}

	// 向上查找textbox父元素
	textboxElem := findTextboxParent(placeholderElem)
	if textboxElem == nil {
		return nil, errors.New("no textbox parent found")
	}

	return textboxElem, nil
}

func findPlaceholderElement(elements []*rod.Element, searchText string) *rod.Element {
	for _, elem := range elements {
		placeholder, err := elem.Attribute("data-placeholder")
		if err != nil || placeholder == nil {
			continue
		}

		if strings.Contains(*placeholder, searchText) {
			return elem
		}
	}
	return nil
}

func findTextboxParent(elem *rod.Element) *rod.Element {
	currentElem := elem
	for i := 0; i < 5; i++ {
		parent, err := currentElem.Parent()
		if err != nil {
			break
		}

		role, err := parent.Attribute("role")
		if err != nil || role == nil {
			currentElem = parent
			continue
		}

		if *role == "textbox" {
			return parent
		}

		currentElem = parent
	}
	return nil
}

// isElementVisible 检查元素是否可见
func isElementVisible(elem *rod.Element) bool {

	// 检查是否有隐藏样式
	style, err := elem.Attribute("style")
	if err == nil && style != nil {
		styleStr := *style

		if strings.Contains(styleStr, "left: -9999px") ||
			strings.Contains(styleStr, "top: -9999px") ||
			strings.Contains(styleStr, "position: absolute; left: -9999px") ||
			strings.Contains(styleStr, "display: none") ||
			strings.Contains(styleStr, "visibility: hidden") ||
			strings.Contains(styleStr, "opacity: 1e-05") {
			return false
		}

		// 精确匹配 opacity: 0（不匹配 0.5、0.1 等）
		if strings.Contains(styleStr, "opacity: 0") {
			// 确认是 opacity: 0 而非 opacity: 0.x
			if matched, _ := regexp.MatchString(`opacity:\s*0(\s|;|$)`, styleStr); matched {
				return false
			}
		}
	}

	// 检查 aria-hidden 属性
	ariaHidden, err := elem.Attribute("aria-hidden")
	if err == nil && ariaHidden != nil && *ariaHidden == "true" {
		return false
	}

	// 检查 tabindex 属性（-1 表示不可聚焦，通常也意味着不可见）
	tabindex, err := elem.Attribute("tabindex")
	if err == nil && tabindex != nil && *tabindex == "-1" {
		// 结合检查是否有 active class 来判断是否是真正的隐藏
		class, _ := elem.Attribute("class")
		// 使用单词边界检查，避免匹配 "inactive" 等
		if class == nil || !hasExactClass(*class, "active") {
			// 不是激活状态的 -1 tabindex 元素，可能是隐藏的叠加层
			return false
		}
	}

	visible, err := elem.Visible()
	if err != nil {
		slog.Warn("无法获取元素可见性", "error", err)
		return true
	}

	return visible
}

// hasExactClass 检查 class 字符串是否包含指定的完整类名（单词边界匹配）
func hasExactClass(classStr, className string) bool {
	pattern := `\b` + regexp.QuoteMeta(className) + `\b`
	matched, _ := regexp.MatchString(pattern, classStr)
	return matched
}

// setVisibility 设置可见范围
// 支持: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
func setVisibility(page *rod.Page, visibility string) error {
	if visibility == "" || visibility == "公开可见" {
		slog.Info("可见范围使用默认：公开可见")
		return nil
	}

	// 支持的选项校验
	supported := map[string]bool{"仅自己可见": true, "仅互关好友可见": true}
	if !supported[visibility] {
		return errors.Errorf("unsupported visibility: %s, supported: 公开可见、仅自己可见、仅互关好友可见", visibility)
	}

	// 点击可见范围下拉框
	dropdown, err := page.Element("div.permission-card-wrapper div.d-select-content")
	if err != nil {
		return errors.Wrap(err, "find visibility dropdown failed")
	}
	if err := dropdown.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "click visibility dropdown failed")
	}
	time.Sleep(500 * time.Millisecond)

	// 在弹窗中查找并点击目标选项
	opts, err := page.Elements("div.d-options-wrapper div.d-grid-item div.custom-option")
	if err != nil {
		return errors.Wrap(err, "find visibility options failed")
	}
	for _, opt := range opts {
		text, err := opt.Text()
		if err != nil {
			continue
		}
		if strings.Contains(text, visibility) {
			if err := opt.Click(proto.InputMouseButtonLeft, 1); err != nil {
				return errors.Wrap(err, "select visibility failed")
			}
			slog.Info("已设置可见范围", "visibility", visibility)
			time.Sleep(200 * time.Millisecond)
			return nil
		}
	}
	return errors.Errorf("visibility option not found: %s", visibility)
}

// setSchedulePublish 设置定时发布时间
func setSchedulePublish(page *rod.Page, t time.Time) error {
	// 1. 点击定时发布开关
	if err := clickScheduleSwitch(page); err != nil {
		return err
	}
	time.Sleep(800 * time.Millisecond)

	// 2. 设置日期时间
	if err := setDateTime(page, t); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)

	return nil
}

// clickScheduleSwitch 点击定时发布开关
func clickScheduleSwitch(page *rod.Page) error {
	switchElem, err := page.Element(".post-time-wrapper .d-switch")
	if err != nil {
		return errors.Wrap(err, "find scheduled publish switch failed")
	}

	if err := switchElem.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "click scheduled publish switch failed")
	}
	slog.Info("已点击定时发布开关")
	return nil
}

// setDateTime 设置日期时间
func setDateTime(page *rod.Page, t time.Time) error {
	dateTimeStr := t.Format("2006-01-02 15:04")

	input, err := page.Element(".date-picker-container input")
	if err != nil {
		return errors.Wrap(err, "find date time input failed")
	}

	if err := input.SelectAllText(); err != nil {
		return errors.Wrap(err, "select date time text failed")
	}
	if err := input.Input(dateTimeStr); err != nil {
		return errors.Wrap(err, "input date time failed")
	}
	slog.Info("已设置日期时间", "datetime", dateTimeStr)

	return nil
}

// setOriginal 设置原创声明
func setOriginal(page *rod.Page) error {
	// 根据小红书创作者页面的实际结构：
	// div.custom-switch-card 包含 span.has-tips 文本为"原创声明"
	// 开关是 div.d-switch 组件

	// 查找包含"原创声明"文本的 custom-switch-card
	switchCards, err := page.Elements("div.custom-switch-card")
	if err != nil {
		return errors.Wrap(err, "find original declaration card failed")
	}

	for _, card := range switchCards {
		text, err := card.Text()
		if err != nil {
			continue
		}

		// 检查是否是原创声明卡片
		if !strings.Contains(text, "原创声明") {
			continue
		}

		// 找到原创声明卡片，查找其中的 d-switch
		switchElem, err := card.Element("div.d-switch")
		if err != nil {
			continue
		}

		// 检查开关是否已打开（go-rod 原生读取 checkbox 状态）
		if switchElementChecked(switchElem) {
			slog.Info("原创声明已开启")
			return nil
		}

		// 点击开关
		if err := switchElem.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return errors.Wrap(err, "click original declaration switch failed")
		}

		time.Sleep(500 * time.Millisecond)

		// 处理原创声明确认弹窗
		if err := confirmOriginalDeclaration(page); err != nil {
			return errors.Wrap(err, "confirm original declaration failed")
		}

		slog.Info("已开启原创声明")
		return nil
	}

	return errors.New("original declaration option not found")
}

// confirmOriginalDeclaration 处理原创声明确认弹窗（go-rod 原生定位与点击，
// 避免 JS 合成 click 产生 isTrusted=false 的自动化特征）。
func confirmOriginalDeclaration(page *rod.Page) error {
	// 等待确认弹窗出现
	time.Sleep(800 * time.Millisecond)

	// 1. 勾选"原创声明须知" checkbox（原生点击）
	if footer := findFooterByText(page, "原创声明须知"); footer != nil {
		checkFooterCheckbox(footer)
	} else {
		slog.Warn("未找到原创声明确认弹窗的 footer")
	}

	time.Sleep(500 * time.Millisecond)

	// 2. 点击"声明原创"按钮
	footer := findFooterByText(page, "声明原创")
	if footer == nil {
		return errors.New("declare original button not found")
	}
	btn, err := footer.Element("button.custom-button")
	if err != nil {
		return errors.New("declare original button not found")
	}

	// 按钮禁用时，尝试再次勾选 checkbox 后重试
	if isButtonDisabled(btn) {
		checkFooterCheckbox(footer)
		time.Sleep(300 * time.Millisecond)
		if isButtonDisabled(btn) {
			return errors.New("declare original button is still disabled")
		}
	}

	if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "click declare original button failed")
	}

	slog.Info("已成功点击声明原创按钮")
	time.Sleep(300 * time.Millisecond)

	return nil
}

// findFooterByText 返回第一个文本包含 text 的 div.footer 元素，未找到返回 nil。
func findFooterByText(page *rod.Page, text string) *rod.Element {
	footers, err := page.Elements("div.footer")
	if err != nil {
		return nil
	}
	for _, f := range footers {
		if t, err := f.Text(); err == nil && strings.Contains(t, text) {
			return f
		}
	}
	return nil
}

// checkFooterCheckbox 勾选 footer 内未选中的 checkbox（go-rod 原生点击）。
func checkFooterCheckbox(footer *rod.Element) {
	cb, err := footer.Element(`div.d-checkbox input[type="checkbox"]`)
	if err != nil {
		return
	}
	if checked, err := cb.Property("checked"); err == nil && checked.Bool() {
		return
	}
	if err := cb.Click(proto.InputMouseButtonLeft, 1); err != nil {
		slog.Warn("勾选原创声明须知 checkbox 失败", "error", err)
	}
}

// isButtonDisabled 判断按钮是否禁用（class 含 disabled 或 disabled 属性为真）。
func isButtonDisabled(btn *rod.Element) bool {
	if cls, err := btn.Attribute("class"); err == nil && cls != nil && strings.Contains(*cls, "disabled") {
		return true
	}
	if v, err := btn.Property("disabled"); err == nil && v.Bool() {
		return true
	}
	return false
}

// switchElementChecked 读取 d-switch 内 checkbox 的选中状态（go-rod 原生，无需 JS）。
func switchElementChecked(switchElem *rod.Element) bool {
	cb, err := switchElem.Element(`input[type="checkbox"]`)
	if err != nil {
		return false
	}
	v, err := cb.Property("checked")
	if err != nil {
		return false
	}
	return v.Bool()
}

// bindProducts 绑定商品到发布内容
func bindProducts(page *rod.Page, products []string) error {
	if len(products) == 0 {
		return nil
	}

	slog.Info("开始绑定商品", "products", products)

	// 点击"添加商品"按钮
	if err := clickAddProductButton(page); err != nil {
		return errors.Wrap(err, "click add product button failed")
	}
	time.Sleep(1 * time.Second)

	// 等待商品选择弹窗出现
	modal, err := waitForProductModal(page)
	if err != nil {
		return errors.Wrap(err, "wait for product modal failed")
	}
	slog.Info("商品选择弹窗已打开")

	// 遍历搜索并选择商品
	var failedProducts []string
	for _, keyword := range products {
		if err := searchAndSelectProduct(page, modal, keyword); err != nil {
			slog.Warn("搜索选择商品失败", "keyword", keyword, "error", err)
			failedProducts = append(failedProducts, keyword)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 点击保存按钮
	slog.Info("准备点击保存按钮")
	if err := clickModalSaveButton(page, modal); err != nil {
		return errors.Wrap(err, "click save button failed")
	}
	slog.Info("保存按钮点击完成，开始等待弹窗关闭")

	// 等待弹窗关闭
	if err := waitForModalClose(page); err != nil {
		slog.Warn("等待弹窗关闭超时", "error", err)
	} else {
		slog.Info("弹窗已关闭")
	}

	if len(failedProducts) > 0 {
		return errors.Errorf("some products not found: %v", failedProducts)
	}

	slog.Info("商品绑定完成", "total", len(products))
	time.Sleep(1000 * time.Millisecond)
	return nil
}

// clickAddProductButton 点击"添加商品"按钮
func clickAddProductButton(page *rod.Page) error {
	slog.Info("开始查找添加商品按钮")

	// 查找包含"添加商品"文本的元素
	spans, err := page.Elements("span.d-text")
	if err != nil {
		return errors.Wrap(err, "find product button text failed")
	}

	for _, span := range spans {
		text, err := span.Text()
		if err != nil {
			continue
		}
		if strings.TrimSpace(text) == "添加商品" {
			slog.Info("找到添加商品文本，向上查找可点击父元素")
			// 向上查找可点击的父元素
			parent := span
			for i := 0; i < 5; i++ {
				p, err := parent.Parent()
				if err != nil {
					break
				}
				parent = p

				tagName, err := parent.Eval(`() => this.tagName.toLowerCase()`)
				if err != nil {
					continue
				}
				tag := tagName.Value.Str()

				// 检查是否为 button 或含 d-button class
				if tag == "button" {
					if err := parent.Click(proto.InputMouseButtonLeft, 1); err != nil {
						return errors.Wrap(err, "click add product button failed")
					}
					slog.Info("已点击添加商品按钮")
					time.Sleep(300 * time.Millisecond) // 确保弹窗动画开始
					return nil
				}

				cls, _ := parent.Attribute("class")
				if cls != nil && strings.Contains(*cls, "d-button") {
					if err := parent.Click(proto.InputMouseButtonLeft, 1); err != nil {
						return errors.Wrap(err, "click add product button failed")
					}
					slog.Info("已点击添加商品按钮")
					time.Sleep(300 * time.Millisecond) // 确保弹窗动画开始
					return nil
				}
			}
		}
	}

	return errors.New("add product button not found, account may not have product feature enabled")
}

// waitForProductModal 等待商品选择弹窗出现
func waitForProductModal(page *rod.Page) (*rod.Element, error) {
	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		modal, err := page.Element(".multi-goods-selector-modal")
		if err == nil && modal != nil {
			visible, _ := modal.Visible()
			if visible {
				slog.Info("商品选择弹窗已出现")
				return modal, nil
			}
		}
		time.Sleep(100 * time.Millisecond) // 缩短轮询间隔，更快响应
	}

	return nil, errors.New("timed out waiting for product selector modal")
}

// searchAndSelectProduct 搜索并选择商品
func searchAndSelectProduct(page *rod.Page, modal *rod.Element, keyword string) error {
	slog.Info("搜索商品", "keyword", keyword)

	// 1. 获取搜索框
	searchInput, err := modal.Element(`input[placeholder="搜索商品ID 或 商品名称"]`)
	if err != nil {
		return errors.Wrap(err, "product search box not found")
	}

	// 2. 清空并输入关键词（使用原生 JS setter + 完整事件）
	if err := searchInput.SelectAllText(); err != nil {
		slog.Warn("选择搜索框文本失败", "error", err)
	}
	time.Sleep(100 * time.Millisecond)

	// 使用 rod Input 输入关键词
	if err := searchInput.Input(keyword); err != nil {
		return errors.Wrap(err, "input search keyword failed")
	}
	time.Sleep(300 * time.Millisecond)

	// 3. 触发搜索（模拟键盘 Enter）
	if err := page.Keyboard.Press(input.Enter); err != nil {
		return errors.Wrap(err, "trigger search failed")
	}

	// 4. 等待搜索结果加载
	time.Sleep(1 * time.Second)

	// 等待 loading 消失（使用与工作代码相同的选择器）
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		loading, err := modal.Element(".goods-list-loading")
		if err != nil || loading == nil {
			break
		}
		visible, _ := loading.Visible()
		if !visible {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 等待商品列表渲染完成（使用与工作代码相同的选择器）
	for time.Now().Before(deadline) {
		productList, err := modal.Element(".goods-list-normal .good-card-container")
		if err == nil && productList != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // 额外等待确保渲染完成

	// 5. 点击第一个商品的 checkbox（使用与工作代码相同的选择器）
	checkbox, err := modal.Element(".goods-list-normal .good-card-container .d-checkbox")
	if err != nil {
		return errors.Wrap(err, "product checkbox not found")
	}

	// 检查是否已经选中
	isChecked, err := checkbox.Eval(`(el) => {
		return el.querySelector('.d-checkbox-simulator.checked') !== null ||
			   el.querySelector('input[type="checkbox"]:checked') !== null;
	}`)
	if err == nil && isChecked.Value.Bool() {
		slog.Info("商品已选中，跳过", "keyword", keyword)
		return nil
	}

	if err := checkbox.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "click product checkbox failed")
	}

	// 6. 随机延迟模拟人为操作（800-1500ms）
	randomDelay := 800 + rand.Intn(700)
	time.Sleep(time.Duration(randomDelay) * time.Millisecond)

	slog.Info("已选择商品", "keyword", keyword)
	return nil
}

// clickModalSaveButton 点击保存按钮
func clickModalSaveButton(page *rod.Page, modal *rod.Element) error {
	// 查找保存按钮（参考工作代码：直接查找并点击，不强制要求找到）
	btn, err := modal.Element(".goods-selected-footer button")
	if err == nil && btn != nil {
		if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
			slog.Warn("点击保存按钮失败", "error", err)
		} else {
			slog.Info("已点击保存按钮")
			return nil
		}
	}

	// 尝试点击主按钮
	primaryBtn, err := modal.Element(".goods-selected-footer .d-button--primary")
	if err == nil && primaryBtn != nil {
		if err := primaryBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
			slog.Warn("点击主按钮失败", "error", err)
		} else {
			slog.Info("已点击主按钮")
			return nil
		}
	}

	slog.Warn("未找到保存按钮，继续执行")
	return nil
}

// waitForModalClose 等待弹窗关闭
func waitForModalClose(page *rod.Page) error {
	deadline := time.Now().Add(5 * time.Second)
	slog.Info("开始等待弹窗关闭")

	for time.Now().Before(deadline) {
		// 使用 Has 代替 Element，避免等待元素出现的阻塞
		has, _, err := page.Has(".multi-goods-selector-modal")
		if err != nil || !has {
			slog.Info("弹窗已关闭")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return errors.New("timed out waiting for modal to close")
}
