# htr

Handwritten Text Recognition

## Install

You can install `htr` using homebrew

```
brew tap lehigh-university-libraries/homebrew https://github.com/lehigh-university-libraries/homebrew
brew install lehigh-university-libraries/homebrew/htr
```

### Download Binary

Instead of homebrew, you can download a binary for your system from [the latest release](https://github.com/lehigh-university-libraries/htr/releases/latest)

Then put the binary in a directory that is in your `$PATH`


## Usage

Set an environment variables `OPENAI_API_KEY` or create it in a `.env` file

### Eval

```
htr eval \
  --model gpt-4o \
  --prompt "Extract all text from this image" \
  --temperature 0.0 \
  --csv fixtures/images.csv \
  --dir /Volumes/2025-Lyrasis-Catalyst-Fund/ground-truth-documents
```


## Updating

### Homebrew

If homebrew was used, you can simply upgrade the homebrew formulae for htr

```
brew update && brew upgrade htr
```

### Download Binary

If the binary was downloaded and added to the `$PATH` updating htr could look as follows. Requires [gh](https://cli.github.com/manual/installation) and `tar`

```
# update for your architecture
ARCH="htr_Linux_x86_64.tar.gz"
TAG=$(gh release list --exclude-pre-releases --exclude-drafts --limit 1 --repo lehigh-university-libraries/htr | awk '{print $3}')
gh release download $TAG --repo lehigh-university-libraries/htr --pattern $ARCH
tar -zxvf $ARCH
mv htr /directory/in/path/binary/was/placed
rm $ARCH
```
