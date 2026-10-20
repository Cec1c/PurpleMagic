"""Query acceptance using real HTTP and TestV1QueriesBrowserMySQL's disposable DB."""
import argparse
import json
import time
from pathlib import Path
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright, expect

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--url', required=True)
parser.add_argument('--output-dir', required=True, type=Path)
args = parser.parse_args()
assert urlparse(args.url).hostname == '127.0.0.1'
args.output_dir.mkdir(parents=True, exist_ok=True)

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page(viewport={'width': 1440, 'height': 1050})
    page.set_default_timeout(15000)
    errors = []
    page.on('pageerror', lambda e: errors.append(str(e)))
    def nav(name):
        page.get_by_role('link', name=name, exact=True).click()
    def settled():
        expect(page.locator('.page-loading')).to_have_attribute('data-loading', 'false')
    def control(delay=200, fail=0):
        assert page.request.post(args.url+'/__fixture/control', data={'delay': delay, 'fail': fail}).status == 204
    try:
        page.goto(args.url+'/console/#quests')
        page.get_by_label('访问 Token', exact=True).fill('fixture')
        page.get_by_label('在此设备记忆 30 天', exact=True).uncheck()
        page.get_by_role('button', name='进入工作台', exact=True).click()
        expect(page.get_by_role('tab', name='任务库', exact=True)).to_have_attribute('aria-selected', 'true')
        expect(page.get_by_text('浏览器任务', exact=True).first).to_be_visible()
        settled()

        control()
        start = time.perf_counter()
        nav('账号与角色')
        expect(page.locator('tbody tr')).to_have_count(20)
        first_rows = time.perf_counter()-start
        settled()
        account_elapsed = time.perf_counter()-start
        metrics = page.request.get(args.url+'/__fixture/metrics').json()
        assert metrics['gets'] == 21, metrics
        assert 2 <= metrics['peak'] <= 4, metrics
        assert first_rows < 1.5 and account_elapsed < 5, (first_rows, account_elapsed)
        expect(page.locator('tbody tr').first).to_contain_text('1,000')
        page.screenshot(path=str(args.output_dir/'accounts-desktop.png'), full_page=True)

        nav('任务'); settled()
        control(3000)
        nav('账号与角色')
        expect(page.locator('tbody tr')).to_have_count(20)
        page.wait_for_timeout(100)
        switched = time.perf_counter()
        nav('任务'); settled()
        switch_elapsed = time.perf_counter()-switched
        assert switch_elapsed < 2, switch_elapsed
        expect(page.get_by_text('浏览器任务', exact=True).first).to_be_visible()

        control(50,1)
        nav('账号与角色');settled()
        expect(page.locator('tbody tr')).to_have_count(20)
        expect(page.get_by_text('测试点券读取失败', exact=True)).to_be_visible()
        settled()
        control(50,0)
        page.get_by_role('button',name='重试',exact=True).click();settled()
        expect(page.get_by_text('测试点券读取失败', exact=True)).to_have_count(0)
        retry_metrics=page.request.get(args.url+'/__fixture/metrics').json()
        assert retry_metrics['gets']==1,retry_metrics

        nav('查询')
        page.get_by_role('tab',name='服务器活动',exact=True).click();settled()
        row=page.locator('tbody tr').filter(has_text='红尾乔纳森')
        expect(row).to_contain_text('等待下次游戏服务重启')
        expect(row).to_contain_text('未运行')
        page.screenshot(path=str(args.output_dir/'activities-desktop.png'),full_page=True)
        page.get_by_role('tab',name='PVF 物品库',exact=True).click()
        page.get_by_placeholder('搜索 PVF 物品名称或 ID',exact=True).fill('7002')
        page.get_by_role('button',name='测试药水',exact=True).click()
        expect(page.get_by_label('PVF 物品详情')).to_contain_text('恢复生命值')
        page.screenshot(path=str(args.output_dir/'items-desktop.png'),full_page=True)

        nav('任务')
        page.get_by_role('button',name='选择操作角色（0）',exact=True).click()
        dialog=page.get_by_role('dialog')
        for role in ('查询测试10','查询测试11'):
            dialog.get_by_label('收件角色 '+role,exact=True).check()
        dialog.get_by_role('button',name='关闭',exact=True).click()
        expect(page.get_by_role('button',name='选择操作角色（2）',exact=True)).to_be_visible()
        page.get_by_label('选择任务 浏览器任务',exact=True).check()
        page.get_by_role('button',name='完成选中',exact=True).click()
        dialog=page.get_by_role('dialog')
        expect(dialog).to_contain_text('2 个角色')
        expect(dialog).to_contain_text('不发任务奖励')
        dialog.get_by_role('button',name='确认执行',exact=True).click()
        dialog.get_by_role('button',name='完成',exact=True).click()
        page.screenshot(path=str(args.output_dir/'quests-desktop.png'),full_page=True)

        nav('公告与下载源')
        page.get_by_role('tab',name='PVF 数据模式',exact=True).click()
        page.get_by_role('button',name='直接加载 PVF',exact=True).click()
        expect(page.get_by_text('当前使用：内存 PVF',exact=True)).to_be_visible()
        page.reload()
        page.get_by_role('tab',name='PVF 数据模式',exact=True).click()
        expect(page.get_by_text('当前使用：内存 PVF',exact=True)).to_be_visible()
        page.get_by_role('button',name='磁盘索引',exact=True).click()
        expect(page.get_by_text('当前使用：磁盘索引',exact=True)).to_be_visible()
        page.screenshot(path=str(args.output_dir/'pvf-desktop.png'),full_page=True)
        page.set_viewport_size({'width':390,'height':844})
        page.screenshot(path=str(args.output_dir/'pvf-mobile-dark.png'),full_page=True)
        assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
        page.get_by_role('button',name='切换浅色主题',exact=True).click()
        page.wait_for_timeout(350)
        page.screenshot(path=str(args.output_dir/'pvf-mobile-light.png'),full_page=True)
        assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
        assert not errors,errors
        report={'passed':True,'errors':errors,'firstAccountRowsSeconds':first_rows,'accountCompleteSeconds':account_elapsed,'accountMetrics':metrics,'switchToQuestSeconds':switch_elapsed,'realHTTPAndDisposableDB':True,'syntheticPVF':True,'browserModeControl':'stub; actual loader covered by platform tests'}
        (args.output_dir/'receipt.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
        print(json.dumps(report,ensure_ascii=False))
    except Exception:
        page.screenshot(path=str(args.output_dir/'failure.png'),full_page=True)
        (args.output_dir/'failure.txt').write_text(page.locator('body').inner_text(),encoding='utf-8')
        raise
    finally:
        browser.close()
