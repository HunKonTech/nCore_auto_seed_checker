# nCore Auto Seed Checker

This is a GO application designed as an automatic seed checker for the nCore.pro website.

## User Guide

1. **URL Verification:** After downloading, verify the URLs in the `main.go` file:
   - `loginUrl`: this should point to `login.php`.
   - `activityUrl`: this should point to `hitnrun.php`.

2. **Filling in Login Information:** nCore shows a reCAPTCHA on the login form, so the recommended way is to log in once on the web interface's **Settings** page (see below). The session cookie is saved to `settings.json` and used for every check.
   Alternatively you can provide your username and password using command line flags (these are also used as a fallback if the saved cookie expires):
   - `-u your_username`
   - `-p your_password`

3. **Setting the Output Directory:**
   - Specify the directory using the `-o` flag.
   - **Note:** This application **does not re-download the torrents**, you need to set your torrent client to download the torrent files into the same folder, where they will be automatically added to the torrent client.

4. **Building the Application:**
   - Required dependencies:
      - `go get github.com/chromedp/chromedp`
      - `go get github.com/chromedp/cdproto/cdp`
   - First build the web interface (WebAssembly) with `go generate`, then:
   - On Windows: `go build -o ncore_automation.exe .`
   - On Linux/Mac: `go build -o ncore_automation .`
   - Run the above command in the directory where the downloaded file is located.

5. **Running the Application:**
   - **One-time Run:** 
     `./ncore_automation -u user -p pass -o ./torrents`
   - **Web Interface:** 
     `./ncore_automation -u user -p pass -o ./torrents -web`
     Open [http://localhost:8080](http://localhost:8080) to view status, history and the live log; the page updates itself over a websocket.
     You can change the port with `-port 9090`.
   - **Continuous Mode (Every 10 min):**
     `./ncore_automation -u user -p pass -o ./torrents -web -interval 10m`

   Use the `-d` flag to enable logging to `log.txt`.

6. **Logging in on the Settings page:**
   - Open the **Settings** tab and click **Log in to nCore**. A browser running on the server opens the nCore login page and streams it into the page; click and type in it as usual, and solve the reCAPTCHA.
   - Once you are logged in, the cookie is saved automatically (or click **Save cookie**). Use **Test cookie** to check it later; the dashboard shows when it has expired.
   - The output directory and the check interval can also be set here (unless given with `-o` / `-interval`).
   - Without the web interface a cookie can also be given in the `NCORE_COOKIE` environment variable (`name=value; name2=value2`).
   - Use `CHROME_PATH` to point to Chrome/Chromium if it is not found automatically.
   - **Note:** the web interface has no password. Do not expose its port to the internet.

---

**Important:** I am not responsible for any illegal content distribution. This application only automates manual steps.

HUN
# nCore Auto Seed Checker

Ez egy nCore.pro oldalhoz készült automatikus seed checker GO alkalmazás.

## Használati útmutató

1. **URL ellenőrzés:** Miután letöltötted, ellenőrizd a `main.go` fájlban:
   - `loginUrl`: ennek a `login.php`-ra kell vezetnie.
   - `activityUrl`: ennek a `hitnrun.php`-ra kell vezetnie.
   
2. **Login adatok megadása:** Az nCore bejelentkezésnél reCAPTCHA van, ezért a javasolt mód, hogy egyszer bejelentkezel a webes felület **Settings** (Beállítások) oldalán (lásd lent). A session cookie a `settings.json`-ba mentődik, és minden ellenőrzés ezt használja.
   Megadhatod a felhasználónevedet és jelszavadat parancssori kapcsolókkal is (ezeket akkor is használja, ha a mentett cookie lejárt):
   - `-u felhasznalonev`
   - `-p jelszo`

3. **Kimeneti könyvtár beállítása:**
   - Az `-o` kapcsolóval adhatod meg, hova kerüljenek a letöltött `.torrent` fájlok.
   - **Figyelem:** Ez az alkalmazás **nem tölti vissza a torrentet**, hanem egy tetszőleges torrent alkalmazásba kell beállítani, hogy ugyanabba a mappába töltse le a torrent fájlokat, ahol automatikusan hozzáadja őket a torrent alkalmazáshoz.

4. **Alkalmazás buildelése:**
   - Szükséges kiegészítők:
      - go get github.com/chromedp/chromedp
      - go get github.com/chromedp/cdproto/cdp 
   - Először buildeld a webes felületet (WebAssembly) a `go generate` paranccsal, utána:
   - Windows rendszeren: `go build -o ncore_automation.exe .`
   - Linux/Mac rendszeren: `go build -o ncore_automation .`
   - Futtasd le a fenti parancsot a letöltött fájl mappájában.

5. **Alkalmazás futtatása:**
   - **Egyszeri futtatás:**
     `./ncore_automation -u user -p pass -o ./torrents`
   - **Web felület:** 
     `./ncore_automation -u user -p pass -o ./torrents -web`
     Megtekintheted a [http://localhost:8080](http://localhost:8080) címen (állapot, előzmények, élő log; websocketen keresztül magától frissül).
     A portot a `-port 9090` kapcsolóval módosíthatod.
   - **Folyamatos futtatás (10 percenként):**
     `./ncore_automation -u user -p pass -o ./torrents -web -interval 10m`

`-d` kapcsolóval log.txt-be logol

6. **Bejelentkezés a Settings oldalon:**
   - Nyisd meg a **Settings** fület, és kattints a **Log in to nCore** gombra. A szerveren futó böngésző megnyitja az nCore bejelentkező oldalát, és élőben megjeleníti az oldalon; ugyanúgy kattinthatsz és gépelhetsz benne, a reCAPTCHA-t is itt oldod meg.
   - Bejelentkezés után a cookie automatikusan elmentődik (vagy kattints a **Save cookie** gombra). A **Test cookie** gombbal később ellenőrizheted; a Dashboard jelzi, ha lejárt.
   - Itt állítható a kimeneti mappa és az ellenőrzési időköz is (ha nem `-o` / `-interval` kapcsolóval adtad meg).
   - Webes felület nélkül a cookie-t a `NCORE_COOKIE` környezeti változóban is megadhatod (`nev=ertek; nev2=ertek2`).
   - Ha a Chrome/Chromium nem található automatikusan, a `CHROME_PATH` változóval adhatod meg az útvonalát.
   - **Figyelem:** a webes felület nincs jelszóval védve, ne tedd elérhetővé az internetről.

---

**Fontos:** Semmilyen felelősséget nem vállalok jogsértő tartalom terjesztéséért, ez az alkalmazás csak a manuális lépéseket váltja ki.
