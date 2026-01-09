import os, re, time, sqlite3, requests

DB_PATH = os.getenv("DB_PATH", "/data/app.db")
INTERVAL = int(os.getenv("CHECK_INTERVAL_SECONDS", "300"))
UA = os.getenv("USER_AGENT", "Mozilla/5.0 (stock-monitor)")
ALWAYS_NOTIFY = os.getenv("ALWAYS_NOTIFY", "false").lower() == "true"

DEFAULT_COUNT_REGEX = r"(\d+)\s+Available"
DEFAULT_OOS_REGEX = r"Out of stock|Sold out|0\s+Available"

def db():
    return sqlite3.connect(DB_PATH)

def get_settings(conn):
    cur = conn.cursor()
    cur.execute("SELECT tg_token, tg_chat_id FROM settings WHERE id=1")
    row = cur.fetchone()
    return (row[0] or "", row[1] or "") if row else ("","")

def send_telegram(token, chat_id, text):
    if not token or not chat_id:
        return
    url = f"https://api.telegram.org/bot{token}/sendMessage"
    requests.post(url, json={"chat_id": chat_id, "text": text, "disable_web_page_preview": True}, timeout=15).raise_for_status()

def fetch(url):
    r = requests.get(url, headers={"User-Agent": UA}, timeout=25)
    r.raise_for_status()
    return r.text

def extract_block(html, keyword, window):
    idx = html.lower().find(keyword.lower())
    if idx == -1:
        return html
    s = max(0, idx - window)
    e = min(len(html), idx + window)
    return html[s:e]

def parse_count(block, count_regex):
    m = re.search(count_regex, block, re.I | re.S)
    if not m:
        return None
    return int(m.group(1))

def check_one(w):
    url, model, count_regex, instock_regex, oos_regex, window = w
    html = fetch(url)
    block = extract_block(html, model, window)

    count_regex = count_regex.strip() if count_regex else DEFAULT_COUNT_REGEX
    instock_regex = instock_regex.strip() if instock_regex else ""
    oos_regex = oos_regex.strip() if oos_regex else DEFAULT_OOS_REGEX

    count = parse_count(block, count_regex)
    if count is not None:
        status = "IN_STOCK" if count > 0 else "OUT_OF_STOCK"
        return status, count, f"[{status}] {model} ({count})\n{url}"

    # fallback: 用关键字判断
    if oos_regex and re.search(oos_regex, block, re.I | re.S):
        status = "OUT_OF_STOCK"
        return status, None, f"[{status}] {model}\n{url}"
    if instock_regex and re.search(instock_regex, block, re.I | re.S):
        status = "IN_STOCK"
        return status, None, f"[{status}] {model}\n{url}"

    # 实在判断不了
    status = "UNKNOWN"
    return status, None, f"[{status}] {model} (regex not matched)\n{url}"

def loop():
    while True:
        try:
            conn = db()
            token, chat_id = get_settings(conn)
            cur = conn.cursor()
            cur.execute("""SELECT url, model, IFNULL(count_regex,''), IFNULL(instock_regex,''), IFNULL(oos_regex,''), window
                           FROM watches WHERE enabled=1""")
            watches = cur.fetchall()

            for w in watches:
                status, available, msg = check_one(w)

                # 查上一状态（用 url+model 定位）
                cur.execute("SELECT id, IFNULL(last_status,''), last_available FROM watches WHERE url=? AND model=?",
                            (w[0], w[1]))
                row = cur.fetchone()
                if not row:
                    continue
                wid, last_status, last_avail = row[0], row[1], row[2]

                changed = (status != last_status) or ((available is not None) and (last_avail != available))

                now = int(time.time())
                cur.execute("""UPDATE watches
                               SET last_status=?, last_available=?, last_checked=?
                               WHERE id=?""", (status, available, now, wid))
                conn.commit()

                if ALWAYS_NOTIFY or changed:
                    send_telegram(token, chat_id, msg)
                    cur.execute("UPDATE watches SET last_notified=? WHERE id=?", (now, wid))
                    conn.commit()

            conn.close()
        except Exception as e:
            # 生产环境你可以把错误也发 TG，我这里先只打印
            print("worker error:", e)

        time.sleep(INTERVAL)

if __name__ == "__main__":
    print("worker started. interval =", INTERVAL, "seconds")
    loop()
