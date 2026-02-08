#!/usr/bin/env python3
import os, sys, time, gzip, shutil
import sqlite3
from datetime import datetime

def die(msg):
    print(msg, file=sys.stderr)
    sys.exit(1)

def main():
    db_path = os.environ.get('EDRMOUNT_DB', '/home/pulgarcito/.openclaw/workspace/edrmount-data/config/edrmount.db')
    out_dir = os.environ.get('EDRMOUNT_BACKUP_DIR', '/home/pulgarcito/.openclaw/workspace/edrmount-backups')
    keep = int(os.environ.get('EDRMOUNT_BACKUP_KEEP', '30'))

    if not os.path.exists(db_path):
        die(f"DB not found: {db_path}")

    os.makedirs(out_dir, exist_ok=True)

    ts = datetime.now().strftime('%Y%m%d-%H%M%S')
    tmp_out = os.path.join(out_dir, f"edrmount.db.{ts}.tmp")
    final_out = os.path.join(out_dir, f"edrmount.db.{ts}.sqlite")
    final_gz = final_out + '.gz'

    # Connect and checkpoint WAL to get a consistent snapshot.
    con = sqlite3.connect(db_path, timeout=30)
    try:
        con.execute('PRAGMA busy_timeout=30000')
        try:
            con.execute('PRAGMA wal_checkpoint(FULL)')
        except Exception:
            pass

        bck = sqlite3.connect(tmp_out)
        try:
            con.backup(bck)
        finally:
            bck.close()
    finally:
        con.close()

    os.replace(tmp_out, final_out)

    # Compress (smaller + friendlier for long-term storage)
    with open(final_out, 'rb') as f_in, gzip.open(final_gz, 'wb', compresslevel=6) as f_out:
        shutil.copyfileobj(f_in, f_out)
    os.remove(final_out)

    print(f"OK: {final_gz}")

    # Rotation: keep newest N backups
    files = sorted(
        [f for f in os.listdir(out_dir) if f.startswith('edrmount.db.') and f.endswith('.sqlite.gz')],
        reverse=True,
    )
    for f in files[keep:]:
        try:
            os.remove(os.path.join(out_dir, f))
        except Exception:
            pass

if __name__ == '__main__':
    main()
