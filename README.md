# OriginCheck: a Turnitin-style checker for student work

OriginCheck checks student papers for copied text and AI-written text, and produces a report like Turnitin's:

- **Similarity report:** an overall similarity %, the matching passages highlighted and numbered in the paper, the list of sources (internet pages, published papers, and other students' papers), Turnitin's four match groups (Not Cited or Quoted, Missing Quotations, Missing Citation, Cited and Quoted), and filters to exclude quotes, the bibliography, cited text, short matches, single sources or single matches.
- **AI writing report:** the % of the paper's prose that is likely AI-generated, with those sentences highlighted. As in Turnitin, at least 300 words of prose are needed, and scores from 1% to 19% show as `*%`.
- **Integrity flags:** letters swapped for look-alikes from other alphabets, and hidden (white or tiny) text in Word files.
- **Compare papers:** checks every pair of papers in an assignment against each other, to spot students sharing work.
- **PDF report** for each paper, to print or save.

It runs on your own Windows computer. Papers stay on your computer; only short search phrases are sent to search engines.

## Start it

1. Download `OriginCheck.zip` from the [Releases page](https://github.com/kayking77/Origin-Check/releases/latest), right-click it and choose **Extract All**.
2. Double-click `origincheck.exe`.
   If Windows SmartScreen warns you, choose **More info → Run anyway** (the app isn't code-signed).
3. A black window opens and your browser opens OriginCheck at `http://127.0.0.1:8430`.
   Leave the black window open while you use it. Close it to stop OriginCheck.

## Check papers

1. Click **New assignment**, give it a name, and choose what to compare against (all are on by default).
2. Drop the students' files (.docx, .pdf, .txt, .rtf or .odt) on the upload box. You can drop a whole class at once; each file's name is used as the student's name (for example `Jane_Doe_essay.docx`). You can also paste text.
3. Each paper shows a progress line while it's checked. Web searches take a minute or two per paper.
4. Click the **Similarity** or **AI writing** score to open the report. Click a highlight or a source to see the matching source text side by side.

## Make web search dependable (recommended, free)

Without a key, OriginCheck searches DuckDuckGo and Bing directly. That works, but search engines may slow it down or block it when you check a lot of papers. For dependable results:

1. Go to https://brave.com/search/api/ and sign up for the **free** plan (it allows a few thousand searches a month; each paper uses up to 40).
2. In OriginCheck, open **Settings**, paste the key into **Brave Search API key**, click **Test web search**, then **Save settings**.

## Use it from your iPad or iPhone

Your laptop already has Tailscale. Start OriginCheck with a password so other devices can reach it:

```
origincheck.exe -addr 0.0.0.0:8430 -password choose-a-password
```

Then on the iPad open `http://<your-laptop-name>:8430` and sign in with any user name and that password.

## Where your data is

Everything is kept in `%APPDATA%\OriginCheck` (assignments, papers, reports, and the repository of stored papers). Copy that folder to back it up. To keep data somewhere else, start it with `origincheck.exe -data D:\OriginCheck`.

## How accurate is it?

Open **How it works** in the app for the full table. In short, on test papers never used to train it:

- Human writing scored 20% or more: 0% of 697 university student essays, 0% of 314 essays by English learners, 2% of 151 essays from an essay website, 2.7% of 111 short stories.
- AI writing scored 20% or more: 99% of ChatGPT (GPT-3.5) essays, 92% of essays by current Claude models (79% for the most capable one), 99% of AI essays passed through a "humanizer" tool.
- It is weak on AI-written stories (20%) and on students' own essays that were rewritten or polished by AI (14%).

The AI samples come from ChatGPT (2023) and Anthropic's Claude models; it hasn't been tested on GPT-4/5 or Gemini essays. No AI detector, including Turnitin's, is reliable enough to prove misconduct on its own. Treat a high score as a reason to talk with the student and look at their drafts, not as proof.

## What it can't match

Turnitin also compares against its private database of more than a billion student papers and against subscription journals. Nobody else can access those, so OriginCheck finds copying from the open web, open scholarly abstracts (OpenAlex), Wikipedia, and the papers you've checked yourself. Keep **Store new papers in the repository** on, and it gets better as you check more classes.

## Build from source

Needs Go 1.24 or newer.

```
go build -o origincheck .                                   # this computer
GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o origincheck.exe .   # Windows
```

`training/` explains how the AI-writing model in `assets/aimodel.bin.gz` was trained and tested.
