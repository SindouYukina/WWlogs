# Security policy

## Supported versions

Security fixes are made for the latest WWlogs release. WWlogs supports only the current game version, so please check that you use the latest release before you report a problem.

## Reporting a vulnerability

Do not post a security problem, exploit details, keys, packet captures or personal information in a public issue, a pull request or any other public place.

- Use GitHub's private vulnerability reporting: on the repository's **Security** tab, choose **Report a vulnerability**. The report is visible only to the maintainer.
- If that option is not available, open an issue that says only that you have a security problem to report and asks for a private way to send it. Do not include any details in that issue.

Please include:

- the WWlogs version and Build number (Settings → About) and your Windows version;
- what the problem allows someone to do, and under which conditions;
- the steps to reproduce it.

There is no bug bounty and no guaranteed response time. The maintainer will reply when they can and agree with you on the fix and on when the problem can be made public. Please give the maintainer reasonable time to release a fix before you disclose the problem.

## What counts as a security problem

- another program, a web page or crafted network traffic making WWlogs run code, read or write files outside its own data, or stop working in a way an attacker can trigger;
- WWlogs exposing data that it should keep on your PC, such as combat history, settings or personal information;
- WWlogs sending data over the network that the README does not describe;
- a vulnerability in a dependency that affects WWlogs as shipped.

Wrong numbers, missing names and other parsing problems are ordinary bugs: please report them with the bug report form.
