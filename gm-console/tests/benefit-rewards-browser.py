"""Browser acceptance against TestV1BenefitRewardsBrowserMySQL only."""
import argparse
import json
from pathlib import Path
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright, expect

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--url', required=True)
parser.add_argument('--output-dir', type=Path, required=True)
args = parser.parse_args()
assert urlparse(args.url).hostname == '127.0.0.1'
args.output_dir.mkdir(parents=True, exist_ok=True)

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page(viewport={'width': 1440, 'height': 1050}, device_scale_factor=1)
    errors = []
    page.on('pageerror', lambda e: errors.append(str(e)))
    # Synthetic fixture supplies item definitions, without growth/quest tables.
    def capabilities(route):
        res = route.fetch()
        if res.status != 200:
            route.fulfill(response=res)
            return
        value = res.json()
        for key in list(value['data']['features']):
            if key.split('.')[0] in ('skillPoints', 'level', 'profession', 'expert-job', 'quests', 'containers', 'vault'):
                value['data']['features'][key] = False
        route.fulfill(response=res, json=value)
    try:
        page.route('**/api/v1/capabilities', capabilities)
        page.goto(args.url+'/console/#query')
        page.get_by_label('访问 Token', exact=True).fill('fixture')
        page.get_by_label('在此设备记忆 30 天', exact=True).uncheck()
        page.get_by_role('button', name='进入工作台', exact=True).click()
        row = page.locator('tr').filter(has_text='福利测试10')
        row.first.get_by_role('button', name='打开工作区', exact=True).click()
        page.get_by_role('link', name='GM', exact=True).click()
        page.get_by_role('tab', name='福利规则', exact=True).click()
        page.get_by_role('button', name='创建规则', exact=True).click()
        form = page.locator('form.benefit-create')

        def confirm():
            modal = page.get_by_role('dialog')
            modal.get_by_role('button', name='确认执行', exact=True).click()
            modal.get_by_role('button', name='完成', exact=True).click(timeout=30000)

        def send(name):
            row = page.locator('tr').filter(has_text=name)
            row.get_by_role('button', name='查看发放情况', exact=True).click()
            modal = page.get_by_role('dialog')
            modal.get_by_role('button', name='继续发放', exact=True).click()
            confirm()

        form.get_by_label('规则名称', exact=True).fill('浏览器点券福利')
        form.get_by_label('奖励类型', exact=True).select_option('cera')
        expect(form.get_by_label('领取次数', exact=True)).to_be_disabled()
        expect(form.get_by_label('领取次数', exact=True)).to_have_value('account')
        form.get_by_label('每个账号获得点券', exact=True).fill('500')
        page.screenshot(path=str(args.output_dir/'cera-desktop.png'), full_page=True)
        form.get_by_role('button', name='创建并启用', exact=True).click()
        expect(page.get_by_role('dialog')).to_contain_text('点券 500')
        confirm()
        send('浏览器点券福利')
        row = page.locator('tr').filter(has_text='浏览器点券福利')
        row.get_by_role('button', name='暂停', exact=True).click(); confirm()
        row.get_by_role('button', name='启用', exact=True).click(); confirm()
        row.get_by_role('button', name='查看', exact=False).click()
        expect(page.get_by_role('dialog')).to_contain_text('余额 1000 → 1500')
        expect(page.get_by_role('dialog')).to_contain_text('已发 2 / 2')
        page.screenshot(path=str(args.output_dir/'cera-receipts.png'), full_page=True)
        page.get_by_role('dialog').get_by_role('button', name='关闭', exact=True).click()

        form.get_by_label('规则名称', exact=True).fill('浏览器药水福利')
        form.get_by_label('奖励类型', exact=True).select_option('item')
        form.get_by_label('查找道具', exact=True).fill('7002')
        expect(form.get_by_label('搜索结果', exact=True)).to_contain_text('测试药水')
        form.get_by_label('搜索结果', exact=True).select_option('7002')
        expect(form.get_by_label('物品 ID', exact=True)).to_have_value('7002')
        form.get_by_label('每次发放数量', exact=True).fill('12')
        page.screenshot(path=str(args.output_dir/'item-desktop.png'), full_page=True)
        form.get_by_role('button', name='创建并启用', exact=True).click()
        expect(page.get_by_role('dialog')).to_contain_text('测试药水（7002）× 12')
        confirm(); send('浏览器药水福利')
        # Direct ID entry is independent of search results; equipment count is checked.
        form.get_by_label('物品 ID', exact=True).fill('7001')
        form.get_by_label('每次发放数量', exact=True).fill('2')
        form.get_by_role('button', name='创建并启用', exact=True).click()
        expect(page.get_by_text('装备每封邮件数量为 1', exact=True)).to_be_visible()
        form.get_by_label('每次发放数量', exact=True).fill('1')
        form.get_by_label('规则名称', exact=True).fill('浏览器装备福利')
        form.get_by_role('button', name='创建并启用', exact=True).click()
        expect(page.get_by_role('dialog')).to_contain_text('测试短剑（7001）× 1')
        confirm(); send('浏览器装备福利')
        page.set_viewport_size({'width': 390, 'height': 844})
        page.screenshot(path=str(args.output_dir/'item-mobile-dark.png'), full_page=True)
        assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
        page.get_by_role('button', name='切换浅色主题', exact=True).click()
        page.wait_for_timeout(350)
        page.screenshot(path=str(args.output_dir/'item-mobile-light.png'), full_page=True)
        assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
        assert not errors, errors
        (args.output_dir/'receipt.json').write_text(json.dumps({'passed': True, 'errors': errors, 'realHTTPAndDisposableDatabase': True, 'syntheticPVF': True}, ensure_ascii=False, indent=2), encoding='utf-8')
    except Exception:
        page.screenshot(path=str(args.output_dir/'failure.png'), full_page=True)
        (args.output_dir/'failure.html').write_text(page.content(), encoding='utf-8')
        raise
    finally:
        browser.close()
print('PASS cera/item create, confirm, delivery, receipts, pause/resume, direct ID, validation, desktop/mobile themes')
