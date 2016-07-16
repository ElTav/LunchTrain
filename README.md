LunchTrain
======================

A HipChat integration primarily intended to help its users collaborate on places to get lunch

You can find an article I've written on ideology behind LunchTrain here -> http://clypd.com/staying-on-track-for-lunchtime/

Usage (from within HipChat, presumes set up with /train as the slash cmd):

<code> /train help </code> Displays the help 

<code> /train start <Destination> <Duration (int, minutes)> </code> Starts a train to <Destination> that leaves in <Duration> mins

<code> /train join <Destination> </code> Joins a train going to the specified destination

<code> /train passengers <Destination> </code> Reports all passengers for the specified destination

<code> /train active </code> Reports a list of all the active trains and the time left on them
