Ethereum Gas Price Notifier
---------------------------

Transactions on the Ethereum network require "gas", and the price of gas
represents the aggregate demand for Ethereum blockspace at a given point in
time.

Like most things in crypto, the price of gas is extremely volatile, so it is
useful make transactions when gas is cheap to save money, as well as to avoid
making transactions when gas is expensive unless really necessary!

This is a simple program designed to run as a regular background task, every
hour or so. It queries the current recommended gas prices from etherscan and
stores the data to disk. Then, comparing to historical data, it decides if
the current gas price is relatively cheap or expensive. When the categorisation
of gas prices as low, average or high changes, it triggers an email notification
to be sent.

By considering the standard deviation of past gas prices, it is able to adjust
to changing volatility in the prices and avoids needing to program and update
arbitrary price targets.

An example .plist file is provided for macOS launchd, this will need to be
edited to use the correct program paths on your system. Cron may be used instead
on Linux.
