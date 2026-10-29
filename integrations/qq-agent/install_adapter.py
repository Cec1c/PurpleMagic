"""Install the fixed-command consumer in QQ Agent 0.4, preserving other source."""
import argparse
from pathlib import Path

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('root',type=Path)
args=parser.parse_args()
path=args.root/'src/app.js'
source=path.read_text(encoding='utf-8')
marker='// PurpleMagic deterministic account-binding dispatcher v1'
if marker not in source:
    anchor="      if (event.message_type === 'group' && event.group_id != null) return ingestMessage('group', String(event.group_id), event);"
    if source.count(anchor)!=1:raise SystemExit('QQ Agent ingress changed; adapter needs review')
    block='''      // PurpleMagic deterministic account-binding dispatcher v1
      const uid = String(event.user_id ?? '');
      if (!isGloballyBlocked(uid) && !(getConfig().blocklist?.[String(event.group_id)] || []).map(String).includes(uid)) {
        const bindingProvider = skillManager.getCapabilityProviders('message.account-binding')[0]?.fn;
        if (bindingProvider) {
          try { if ((await bindingProvider({ event, onebot, sender }))?.handled) return; }
          catch { log('[binding] fixed command dispatch failed'); return; }
        }
      }
'''
    backup=path.with_suffix('.js.pre-purplemagic-binding')
    if backup.exists():raise SystemExit('Existing adapter backup must be reviewed')
    backup.write_bytes(path.read_bytes())
    path.write_text(source.replace(anchor,block+anchor),encoding='utf-8')
print('QQ Agent account-binding consumer ready')

source=path.read_text(encoding='utf-8')
marker2='// PurpleMagic deterministic moderation dispatcher v1'
if marker2 not in source:
    replacements=[
      ("  let obPortPollTimer = null;", "  let purpleModerationTimer = null;\n  let obPortPollTimer = null;"),
      ("    if (!event || typeof event !== 'object') return;", '''    if (!event || typeof event !== 'object') return;
    // PurpleMagic deterministic moderation dispatcher v1
    if (event.post_type === 'request' || event.post_type === 'notice') {
      const provider = skillManager.getCapabilityProviders('moderation.qq-event')[0]?.fn;
      if (provider) {
        try { if ((await provider({event, onebot}))?.handled) return; }
        catch { log('[binding] group moderation event failed'); }
      }
    }'''),
      ("    await onebot.connect();", '''    await onebot.connect();
    purpleModerationTimer = setInterval(async () => {
      const provider = skillManager.getCapabilityProviders('moderation.qq-tick')[0]?.fn;
      if (provider) {
        try { await provider({onebot}); }
        catch { log('[binding] group moderation will retry'); }
      }
    }, 15000);
    purpleModerationTimer.unref?.();'''),
      ("    if (obPortPollTimer) clearInterval(obPortPollTimer);", "    if (purpleModerationTimer) clearInterval(purpleModerationTimer);\n    if (obPortPollTimer) clearInterval(obPortPollTimer);")
    ]
    for anchor,replacement in replacements:
        if source.count(anchor)!=1:raise SystemExit('QQ Agent moderation anchor changed; adapter needs review')
        source=source.replace(anchor,replacement)
    backup=path.with_suffix('.js.pre-purplemagic-moderation')
    if backup.exists():raise SystemExit('Existing moderation adapter backup must be reviewed')
    backup.write_bytes(path.read_bytes())
    path.write_text(source,encoding='utf-8')
print('QQ Agent moderation consumer ready')
